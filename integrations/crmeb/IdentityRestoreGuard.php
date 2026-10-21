<?php
declare(strict_types=1);
namespace OmniChat;

// Run only with support ingress and token issuance stopped. Never opens traffic.
final class IdentityRestoreGuard
{
    public static function inspect(\PDO $db, string $database, string $prefix): array
    {
        if (!preg_match('/^[A-Za-z0-9_]{1,64}$/D',$database) || !preg_match('/^[A-Za-z0-9_]{0,20}$/D',$prefix)) throw new \RuntimeException('invalid_target');
        if ($db->query('SELECT DATABASE()')->fetchColumn() !== $database) throw new \RuntimeException('wrong_database');
        $table=$prefix.'omni_identity_revision';
        $normalize=static function(string $s):string{
            // Preserve quoted literals: 'ke fu' must not compare equal to 'kefu'.
            // These generated trigger definitions contain no escaped literals.
            $parts=preg_split("/('[^']*')/",$s,-1,PREG_SPLIT_DELIM_CAPTURE);
            foreach($parts as $i=>$part)if($i%2===0)$parts[$i]=strtolower(preg_replace('/[\s`]+/','',$part));
            return implode('',$parts);
        };
        $q=$db->prepare('SELECT TRIGGER_NAME,EVENT_MANIPULATION,EVENT_OBJECT_TABLE,ACTION_TIMING,ACTION_STATEMENT FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=?');
        $q->execute([$database]);$actual=[];
        foreach($q->fetchAll(\PDO::FETCH_ASSOC) as $row)$actual[$row['TRIGGER_NAME']]=$row;
        $checked=[];
        foreach([['api',$prefix.'user','uid'],['kefu',$prefix.'store_service','id']] as [$realm,$source,$pk]){
            foreach(['INSERT','UPDATE','DELETE'] as $event){
                $name=$prefix.'omni_rev_'.$realm.'_'.strtolower($event);
                $ref=$event==='DELETE'?'OLD':'NEW';
                $write="INSERT INTO `$table` (realm,subject_id,revision) VALUES ('$realm',$ref.`$pk`,UUID()) ON DUPLICATE KEY UPDATE revision=UUID();";
                $when=$realm==='api'?'NOT (OLD.status <=> NEW.status) OR NOT (OLD.is_del <=> NEW.is_del)':'NOT (OLD.status <=> NEW.status)';
                $body='BEGIN '.($event==='UPDATE'?"IF $when THEN $write END IF;":$write).' END';
                $row=$actual[$name]??[];
                if(($row['EVENT_MANIPULATION']??'')!==$event || ($row['EVENT_OBJECT_TABLE']??'')!==$source || ($row['ACTION_TIMING']??'')!=='AFTER' || $normalize($row['ACTION_STATEMENT']??'')!==$normalize($body)) throw new \RuntimeException('missing_or_changed_trigger');
                $checked[]=$name;
            }
            $missing=$db->query("SELECT COUNT(*) FROM `$source` s LEFT JOIN `$table` r ON r.realm='$realm' AND r.subject_id=s.`$pk` WHERE r.subject_id IS NULL")->fetchColumn();
            if((int)$missing!==0)throw new \RuntimeException('missing_identity_revision');
        }
        foreach([$prefix.'user',$prefix.'store_service',$table] as $name){
            $q=$db->prepare('SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=?');$q->execute([$database,$name]);
            if(strtoupper((string)$q->fetchColumn())!=='INNODB')throw new \RuntimeException('transactional_storage_required');
        }
        return ['database'=>$database,'revision_rows'=>(int)$db->query("SELECT COUNT(*) FROM `$table`")->fetchColumn(),'triggers_checked'=>count($checked)];
    }

    public static function rotate(\PDO $db, string $database, string $prefix, bool $supportOffline=false): array
    {
        if(!$supportOffline)throw new \RuntimeException('support_offline_confirmation_required');
        if($db->inTransaction())throw new \RuntimeException('unexpected_transaction');
        $plan=self::inspect($db,$database,$prefix);
        $db->beginTransaction();
        try {
            // Includes tombstones: deleting these could resurrect reused IDs.
            $count=$db->exec("UPDATE `{$prefix}omni_identity_revision` SET revision=UUID()");
            if($count!==$plan['revision_rows'])throw new \RuntimeException('rotation_count_mismatch');
            $db->commit();
        }catch(\Throwable $e){if($db->inTransaction())$db->rollBack();throw $e;}
        return $plan+['rotated'=>$count,'support_may_open'=>false,'next'=>'verify_old_credentials_denied_and_fresh_login_before_opening'];
    }
}
