<?php
// CRMEB local CLI only. No implicit DDL; --apply is required.
declare(strict_types=1);
if (PHP_SAPI !== 'cli') exit(1);
require '/www/CRMEB/crmeb/vendor/autoload.php';
$app = new \think\App('/www/CRMEB/crmeb/');
$app->initialize();
use think\facade\Db;
try {
    $user = (new \app\model\user\User())->getTable();
    $staff = (new \app\model\service\StoreService())->getTable();
    if (substr($user,-4)!=='user') throw new RuntimeException('table_prefix');
    $prefix = substr($user,0,-4);
    if ($staff !== $prefix.'store_service') throw new RuntimeException('table_prefix');
    $table = $prefix.'omni_identity_revision';
    foreach ([$user,$staff,$table] as $name) if (!preg_match('/^[A-Za-z0-9_]{1,50}$/D',$name)) throw new RuntimeException('table_name');
    $database = Db::query('SELECT DATABASE() AS name')[0]['name'];
    foreach ([$user,$staff] as $name) {
        $engine = Db::query('SELECT ENGINE FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=?',[$database,$name]);
        if (count($engine)!==1 || strtoupper($engine[0]['ENGINE'])!=='INNODB') throw new RuntimeException('transactional_source_required');
    }
    $exists = Db::query('SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA=? AND TABLE_NAME=?',[$database,$table]);
    $defs=[];
    foreach ([['api',$user,'uid'],['kefu',$staff,'id']] as [$realm,$source,$pk]) {
        foreach (['INSERT','UPDATE','DELETE'] as $event) {
            $trigger=$prefix.'omni_rev_'.$realm.'_'.strtolower($event);
            $ref=$event==='DELETE'?'OLD':'NEW';
            $write="INSERT INTO `$table` (realm,subject_id,revision) VALUES ('$realm',$ref.`$pk`,UUID()) ON DUPLICATE KEY UPDATE revision=UUID();";
            $when=$realm==='api'?'NOT (OLD.status <=> NEW.status) OR NOT (OLD.is_del <=> NEW.is_del)':'NOT (OLD.status <=> NEW.status)';
            $body=$event==='UPDATE'?"IF $when THEN $write END IF;":$write;
            $defs[$trigger]="CREATE TRIGGER `$trigger` AFTER $event ON `$source` FOR EACH ROW BEGIN $body END";
        }
    }
    $existing=Db::query('SELECT TRIGGER_NAME FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA=?',[$database]);
    $names=array_column($existing,'TRIGGER_NAME');
    $collisions=array_values(array_intersect(array_keys($defs),$names));
    if (($argv[1]??'')!=='--apply') {
        echo json_encode(['mode'=>'plan','revision_table_exists'=>(bool)$exists,'trigger_collisions'=>$collisions,'new_triggers'=>array_keys($defs),'source_tables'=>[$user,$staff],'other_trigger_count'=>count($names)]).PHP_EOL;
        exit(0);
    }
    if ($exists || $collisions) throw new RuntimeException('existing_objects_refuse_overwrite');
    // DDL autocommits. On partial failure leave support disabled and inspect;
    // never delete a revision table to retry, as that could revive old tokens.
    Db::execute("CREATE TABLE `$table` (realm VARCHAR(16) NOT NULL, subject_id BIGINT UNSIGNED NOT NULL, revision CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, PRIMARY KEY (realm,subject_id)) ENGINE=InnoDB");
    foreach ($defs as $ddl) Db::execute($ddl);
    // Triggers precede backfill. IGNORE preserves revisions from concurrent
    // updates/deletes/inserts, including a tombstone for a reused primary key.
    Db::execute("INSERT IGNORE INTO `$table` (realm,subject_id,revision) SELECT 'api',uid,UUID() FROM `$user`");
    Db::execute("INSERT IGNORE INTO `$table` (realm,subject_id,revision) SELECT 'kefu',id,UUID() FROM `$staff`");
    echo json_encode(['applied'=>true,'table'=>$table,'triggers'=>array_keys($defs),'new_schema_only'=>true]).PHP_EOL;
} catch (\Throwable $e) {
    $label=$e instanceof RuntimeException && preg_match('/^[a-z_]+$/D',$e->getMessage())?$e->getMessage():'migration_failed_inspect_private_state';
    echo json_encode(['applied'=>false,'error'=>$label]).PHP_EOL;exit(1);
}
