<?php
// CLI-only protocol driver: test the PHP callback without a CRMEB installation.
if (PHP_SAPI!=='cli') exit(1);
require __DIR__.'/../crmeb/OrderShipping.php';
require __DIR__.'/../crmeb/CustomerRead.php';
$x=json_decode(stream_get_contents(STDIN),true,16,JSON_THROW_ON_ERROR);
$request=['customerID'=>$x['customerID'],'content'=>$x['content'],'previousExchange'=>static function()use($x){
    if (!array_key_exists('previous',$x)) {echo json_encode(['need'=>'previous']);exit;}
    return $x['previous'];
}];
$result=\OmniChat\OrderShipping::read($request,static function($uid,$number)use($x){
    if (!array_key_exists('row',$x)) {echo json_encode(['need'=>'lookup','customerID'=>$uid,'number'=>$number]);exit;}
    if (!empty($x['failed'])) throw new RuntimeException('fixture unavailable');
    return $x['row'];
});
\OmniChat\CustomerRead::text($result); // Validate with the actual PHP host boundary.
echo json_encode(['result'=>$result],JSON_THROW_ON_ERROR);
