<?php
declare(strict_types=1);
// Operator CLI. Credentials are stdin-only, never application defaults.
require __DIR__.'/IdentityRestoreGuard.php';
try {
    if(PHP_SAPI!=='cli')throw new RuntimeException('cli_only');
    $apply=false;$offline=false;$target=null;
    foreach(array_slice($argv,1) as $arg){
        if($arg==='--apply'&&!$apply)$apply=true;
        elseif($arg==='--confirm-support-offline'&&!$offline)$offline=true;
        elseif(strpos($arg,'--database=')===0&&$target===null)$target=substr($arg,11);
        else throw new RuntimeException('invalid_arguments');
    }
    if(!$target||($apply&&!$offline)||(!$apply&&$offline))throw new RuntimeException('explicit_target_and_offline_confirmation_required');
    $raw=stream_get_contents(STDIN,65537);
    if(strlen($raw)>65536)throw new RuntimeException('input_limit');
    $c=json_decode($raw,true);unset($raw);
    if(!is_array($c)||array_diff(array_keys($c),['host','port','database','username','password','prefix'])||count($c)!==6)throw new RuntimeException('invalid_configuration');
    foreach(['host','database','username','password','prefix'] as $key)if(!is_string($c[$key]))throw new RuntimeException('invalid_configuration');
    if($c['database']!==$target||!preg_match('/^[A-Za-z0-9_.-]{1,253}$/D',$c['host'])||!is_int($c['port'])||$c['port']<1||$c['port']>65535||!preg_match('/^[A-Za-z0-9_]{1,64}$/D',$target))throw new RuntimeException('invalid_configuration');
    $db=new PDO('mysql:host='.$c['host'].';port='.$c['port'].';dbname='.$target.';charset=utf8mb4',$c['username'],$c['password'],[PDO::ATTR_ERRMODE=>PDO::ERRMODE_EXCEPTION,PDO::ATTR_TIMEOUT=>10]);
    $result=$apply?\OmniChat\IdentityRestoreGuard::rotate($db,$target,$c['prefix'],$offline):\OmniChat\IdentityRestoreGuard::inspect($db,$target,$c['prefix']);
    echo json_encode(['passed'=>true,'mode'=>$apply?'rotate':'plan','result'=>$result]).PHP_EOL;
}catch(Throwable $e){
    $label=$e instanceof RuntimeException&&preg_match('/^[a-z_]+$/D',$e->getMessage())?$e->getMessage():'restore_guard_failed';
    echo json_encode(['passed'=>false,'error'=>$label]).PHP_EOL;exit(1);
}
