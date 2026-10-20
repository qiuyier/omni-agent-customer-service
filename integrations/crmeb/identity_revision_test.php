<?php
declare(strict_types=1);
require __DIR__.'/IdentityRevision.php';
use OmniChat\IdentityRevision as R;
$passed=0;
function check($ok,$name){global $passed;if(!$ok)throw new RuntimeException($name);$passed++;}
function denied(callable $fn,$name){try{$fn();}catch(\Throwable $e){check(true,$name);return;}check(false,$name);}
$v1='11111111-1111-1111-1111-111111111111';
$v2='22222222-2222-2222-2222-222222222222';
$v3='33333333-3333-3333-3333-333333333333';
foreach(['api','kefu'] as $realm){
 $claims=R::stamp(7,$realm,['jti'=>['id'=>7,'type'=>$realm],R::CLAIM=>'forged'],static function()use($v1){return $v1;});
 check($claims[R::CLAIM]===$v1,'server overwrites caller claim');
 R::checkClaims($claims,$realm,7,$v1);check(true,'fresh accepted');
 denied(static function()use($claims,$realm,$v2){R::checkClaims($claims,$realm,7,$v2);},'disabled rotation');
 denied(static function()use($claims,$realm,$v3){R::checkClaims($claims,$realm,7,$v3);},'untouched old token after restore');
 denied(static function()use($claims,$realm,$v1){R::checkClaims($claims,$realm,8,$v1);},'wrong subject');
 denied(static function()use($claims,$v1){R::checkClaims($claims,'admin',7,$v1);},'wrong realm');
 denied(static function()use($realm,$v1){R::checkClaims(['jti'=>['id'=>7,'type'=>$realm]],$realm,7,$v1);},'legacy missing claim');
 $missing=R::stamp(7,$realm,$claims,static function(){throw new RuntimeException('secret');});
 check(!isset($missing[R::CLAIM]),'native login continues without support claim on storage error');
 denied(static function()use($missing,$realm,$v1){R::checkClaims($missing,$realm,7,$v1);},'missing storage never grants support');
 $bad=R::stamp(7,$realm,$claims,static function(){return '';});
 check(!isset($bad[R::CLAIM]),'invalid stored revision removed');
}
check(!isset(R::stamp(7,'admin',[R::CLAIM=>$v1])[R::CLAIM]),'other realm not opted in');
echo json_encode(['passed'=>$passed]).PHP_EOL;
