<?php
declare(strict_types=1);
require __DIR__.'/OrderShipping.php';
use OmniChat\OrderShipping as S;
// Frozen acceptance phrases; exercise the classification boundary without a database.
$cases = [
 ['麻烦帮我看下，我刚买的那单发出了没有？','latest'],
 ['最后下的那笔订单，已经寄出了吗','latest'],
 ['想问下我的订单发了没呀','clarify'],
 ['订单号 ABC-09 发货了没？','number'],
 ['一般下单以后多久能寄出？','other'],
 ['你们周末会发货吗？','other'],
 ['订单退货怎么寄回去','other'],
 ['最近那笔','follow_latest'],
 ['就查最新的那单吧','follow_latest'],
 ['订单号：AB-123','follow_number'],
 ['谢谢','other'],
];
foreach ($cases as [$q,$want]) {
    if (S::intent($q)[0] !== $want) throw new RuntimeException($q);
}
echo count($cases)." shipping dialogue checks passed\n";

// Extra phrases checked after implementing the grammar.
$more = [
 ['请帮我看看，刚刚买的那笔订单寄走了吗？','latest'],
 ['我最新的订单现在有没有发货','latest'],
 ['我的单子发出去了没','clarify'],
 ['订单号 A-23 现在发货了没有？','number'],
 ['一般付款后几天能收到包裹','other'],
 ['节假日订单会延迟发货吗','other'],
 ['订单号 A,23 发货了吗','other'],
];
foreach ($more as [$q,$want]) if (S::intent($q)[0]!==$want) throw new RuntimeException($q);
require_once __DIR__.'/CustomerRead.php';
$u=['id'=>'m1','role'=>'customer','content'=>'我的订单寄出了吗'];
$b=['role'=>'bot','content'=>S::ASK,'reply_to_message_id'=>'m1'];
$h=['conversation_id'=>'one','status'=>'bot_active','messages'=>[$u,$b]];
if (!\OmniChat\CustomerRead::previous($h,'one') || \OmniChat\CustomerRead::previous($h,'two')) throw new RuntimeException('conversation boundary');
foreach (['waiting_human','human_active','closed'] as $state) {
    if (\OmniChat\CustomerRead::previous(['status'=>$state]+$h,'one')) throw new RuntimeException('state boundary');
}
foreach ([[$u,$b,$u],[$u,['role'=>'customer']+$b],[$u,['reply_to_message_id'=>'old']+$b]] as $messages) {
    if (\OmniChat\CustomerRead::previous(['messages'=>$messages]+$h,'one')) throw new RuntimeException('outdated or unbound prompt');
}
// These paths must not even fetch history; without the DB class a lookup would return a failure answer.
$unexpected=static function(){throw new RuntimeException('unexpected history access');};
$answer=static function($uid,$text,$previous){return \OmniChat\CustomerRead::text(S::read(['customerID'=>$uid,'content'=>$text,'previousExchange'=>$previous]));};
if ($answer('901','你们一般多久发货？',$unexpected)!==null ||
    $answer('901','我的订单发货了吗？',$unexpected)!==S::ASK ||
    $answer('901','最近那笔',static function(){return null;})!==null) throw new RuntimeException('unexpected query');
echo "extra phrases, routing and history boundaries passed\n";
