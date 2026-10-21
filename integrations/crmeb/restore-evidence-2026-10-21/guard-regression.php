<?php
declare(strict_types=1);
use think\facade\Config;
use think\facade\Db;
require '/www/CRMEB/crmeb/vendor/autoload.php';
$app=new class('/www/CRMEB/crmeb/') extends \think\App {
 public function loadEnv(string $envName=''):void {
  parent::loadEnv($envName);
  $s=json_decode(file_get_contents('/lab/settings.json'),true);
  $this->env->set('cache.driver','file');
  $this->env->set('database.hostname',$s['mysql']);
  $this->env->set('database.database',$s['database']);
  $this->env->set('database.username','root');
  $this->env->set('database.password',$s['password']);
  $this->env->set('database.hostport',3306);
  $this->env->set('database.debug',false);
  $this->env->set('app.app_key',$s['signing_key']);
 }
};
$app->setRuntimePath('/tmp/restore-runtime/');
$app->initialize();
$settings=json_decode(file_get_contents('/lab/settings.json'),true);
$dbName=$settings['database'];
Config::set(['default'=>'mysql','connections'=>['mysql'=>['type'=>'mysql','hostname'=>$settings['mysql'],'hostport'=>3306,'database'=>$dbName,'username'=>'root','password'=>$settings['password'],'charset'=>'utf8mb4','prefix'=>'eb_','debug'=>false,'deploy'=>0,'rw_separate'=>false,'fields_strict'=>true]]],'database');
Config::set(['default'=>'file','stores'=>['file'=>['type'=>'File','path'=>'/tmp/restore-cache/','prefix'=>'restore:','expire'=>0]]],'cache');
// Do not use the source instance signing key for drill credentials.
\think\facade\Env::set('app.app_key',$settings['signing_key']);
require '/www/CRMEB/crmeb/extend/omni_chat/IdentityRevision.php';
require '/lab/IdentityRestoreGuard.php';
$checks=[];$stage='setup';
function ensure($ok,$name){global $checks;if(!$ok)throw new RuntimeException($name);$checks[]=$name;}
function authorized($t,$realm,$id){
 try{
  $j=app()->make(\crmeb\utils\JwtAuth::class);$j->parseToken($t);$j->verifyToken();
  \OmniChat\IdentityRevision::assertVerifiedToken($t,$realm,$id);return true;
 }catch(\Throwable $e){return false;}
}
try{
 $pdo=new PDO('mysql:host='.$settings['mysql'].';dbname='.$dbName,'root',$settings['password'],[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION]);
 ensure(Db::query('SELECT DATABASE() AS name')[0]['name']===$dbName,'isolated_database_selected');
 ensure($dbName!=='crmeb','source_database_not_target');
 $plan=\OmniChat\IdentityRestoreGuard::inspect($pdo,$dbName,'eb_');
 ensure($plan['triggers_checked']===6,'all_restored_triggers_exact');
 $subjects=[['api',(int)Db::name('user')->where('status',1)->where('is_del',0)->value('uid'),'user','uid'],['kefu',(int)Db::name('store_service')->where('status',1)->value('id'),'store_service','id']];
 $old=[];$revisions=[];
 foreach($subjects as [$realm,$id,$table,$pk]){
  ensure($id>0,$realm.'_enabled_subject_exists');
  $revisions[$realm]=\OmniChat\IdentityRevision::current($realm,$id);
  $old[$realm]=app()->make(\crmeb\utils\JwtAuth::class)->createToken($id,$realm,['test_nonce'=>bin2hex(random_bytes(16))])['token'];
  ensure(authorized($old[$realm],$realm,$id),$realm.'_old_credential_initially_valid');
  Db::name($table)->where($pk,$id)->update(['status'=>0]);
  Db::name($table)->where($pk,$id)->update(['status'=>1]);
  ensure(!authorized($old[$realm],$realm,$id),$realm.'_revoked_before_restore');
  // Restore the exact version read from the backup, only in this isolated copy.
  // This models a backup predating revocation with still-valid login material.
  Db::name('omni_identity_revision')->where(['realm'=>$realm,'subject_id'=>$id])->update(['revision'=>$revisions[$realm]]);
  ensure(authorized($old[$realm],$realm,$id),$realm.'_unsafe_old_snapshot_revives_permission');
 }
 $stage='restore_guard';
 try{\OmniChat\IdentityRestoreGuard::rotate($pdo,$dbName,'eb_');throw new RuntimeException('consent_missing');}
 catch(RuntimeException $e){ensure($e->getMessage()==='support_offline_confirmation_required','rotation_requires_offline_ack');}
 // Failure path: missing trigger blocks rotation. Restore its original DDL.
 $trigger='eb_omni_rev_kefu_update';$definition=$pdo->query("SHOW CREATE TRIGGER `$trigger`")->fetch(PDO::FETCH_ASSOC);
 $ddl=$definition['SQL Original Statement'];
 $pdo->exec("DROP TRIGGER `$trigger`");
 try{
  try{\OmniChat\IdentityRestoreGuard::rotate($pdo,$dbName,'eb_',true);throw new RuntimeException('missing_trigger_not_blocked');}
  catch(RuntimeException $e){ensure($e->getMessage()==='missing_or_changed_trigger','missing_trigger_blocks_rotation');}
 }finally{$pdo->exec($ddl);}
 $changed=str_replace("VALUES ('kefu'","VALUES ('ke fu'",$ddl);
 ensure($changed!==$ddl,'changed_trigger_fixture_prepared');
 $pdo->exec("DROP TRIGGER `$trigger`");
 try{
  $pdo->exec($changed);
  try{\OmniChat\IdentityRestoreGuard::inspect($pdo,$dbName,'eb_');throw new RuntimeException('changed_trigger_not_blocked');}
  catch(RuntimeException $e){ensure($e->getMessage()==='missing_or_changed_trigger','changed_trigger_literal_denied');}
 }finally{$pdo->exec("DROP TRIGGER IF EXISTS `$trigger`");$pdo->exec($ddl);}
 $rotated=\OmniChat\IdentityRestoreGuard::rotate($pdo,$dbName,'eb_',true);
 ensure($rotated['rotated']===$plan['revision_rows'],'all_versions_rotated');
 foreach($subjects as [$realm,$id,$table,$pk]){
  ensure(!authorized($old[$realm],$realm,$id),$realm.'_restored_old_credential_denied_after_guard');
  $fresh=app()->make(\crmeb\utils\JwtAuth::class)->createToken($id,$realm,['test_nonce'=>bin2hex(random_bytes(16))])['token'];
  ensure(authorized($fresh,$realm,$id),$realm.'_fresh_signed_login_accepted');
 }
 echo json_encode(['passed'=>true,'checks'=>$checks,'rotated_rows'=>$rotated['rotated'],'scope'=>'isolated restored MySQL + actual JwtAuth/IdentityRevision; synthetic signed login credentials; not browser/password login','external_model_calls'=>0]).PHP_EOL;
}catch(Throwable $e){
 echo json_encode(['passed'=>false,'checks'=>$checks,'failed_stage'=>$stage,'error'=>$e instanceof RuntimeException&&preg_match('/^[a-z_]+$/D',$e->getMessage())?$e->getMessage():'restore_verification_failed']).PHP_EOL;exit(1);
}
