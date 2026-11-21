<?php
declare(strict_types=1);
namespace OmniChat;

/** One opt-in host capability. No prompt, credentials, free SQL or raw DB text. */
final class OrderShipping
{
    public static function answer(string $uid, string $text): ?string
    {
        $q = preg_replace('/\s+/u', '', trim($text));
        $latest = '/^(?:请问|帮我查一下|查一下)?(?:我(?:的)?)?(?:最近|最新|最后)(?:的)?(?:一[笔单个张])?(?:的)?订单(?:现在)?(?:发货了吗|发货了没|发货没|发货了没有|发货状态|有没有发货|是否已?发货)[？?。！!]?$/u';
        $specific = '/^(?:请问|帮我查一下|查一下)?(?:我的)?订单(?:号)?[:：#]?([a-zA-Z0-9_-]{1,32})(?:现在)?(?:发货了吗|发货了没|发货没|发货状态|是否已?发货)[？?。！!]?$/u';
        if (preg_match($specific, $q, $match)) $number = $match[1];
        elseif (preg_match($latest, $q)) $number = null;
        else return null;
        try {
            $query = \think\facade\Db::name('store_order')->where(['uid'=>$uid,'is_del'=>0,'is_system_del'=>0]);
            if ($number !== null) $query = $query->where('order_id',$number);
            $row = $query->field('paid,is_cancel,refund_status,shipping_type,status')->order('add_time desc,id desc')->find();
            if (!$row) return '没有找到你可查看的订单记录。';
            if ((int)$row['is_cancel']) return '这笔订单已取消。';
            if ((int)$row['refund_status'] !== 0) return '这笔订单涉及售后，暂不能确认发货状态。';
            if ((int)$row['shipping_type'] !== 1) return '这不是快递配送订单，不能按快递发货状态查询。';
            if (!(int)$row['paid']) return '这笔订单未支付，暂不能确认发货状态。';
            $states = [0=>'这笔订单尚未发货。',1=>'这笔订单已发货，当前待收货。',2=>'这笔订单已收货。',3=>'这笔订单已完成。',4=>'这笔订单已部分发货。'];
            return $states[(int)$row['status']] ?? '暂时无法确认这笔订单的发货状态。';
        } catch (\Throwable $e) { return '暂时查不到订单发货状态，请稍后再试。'; }
    }
}
