<?php
declare(strict_types=1);
namespace OmniChat;

/** One opt-in host capability. No prompt, credentials, free SQL or raw DB text. */
final class OrderShipping
{
    const ASK = '要查最近一笔订单，还是按订单号查？你可以回复“最近那笔”或“订单号：…”。';

    // Classify before reading either the order DB or the conversation history.
    public static function intent(string $text): array
    {
        $q = preg_replace('/\s+/u', '', trim($text));
        $q = preg_replace('/(?<=\p{Han})[，,](?=\p{Han})/u', '', $q);
        $q = preg_replace('/[呀啊嘛呢吧？?。！!]+$/u', '', $q);
        $q = preg_replace('/^(?:麻烦|请问|请|想问下|想问一下|帮我|给我|查一下|查下|查查|看看|看一下|看下)+/u', '', $q);
        $selector = '(?:最近|最新|最后|刚刚|刚)(?:一次)?(?:买|下单|下)?(?:的)?(?:那|这|一)?(?:笔|个|张|单)?(?:的)?';
        $ship = '(?:现在)?(?:已经|有没有|是否已?)?(?:发货|发出|寄出|寄走|寄|发)(?:来|去)?(?:了)?(?:吗|了没|没有|没|状态)?';
        $id = '([A-Za-z0-9_-]{1,32})';
        if (preg_match('~^(?:我的)?订单(?:号)?[:：#]?'.$id.$ship.'$~uD', $q, $m)) return ['number',$m[1]];
        if (preg_match('~^(?:我的?)?'.$selector.'(?:订单|单)?'.$ship.'$~uD', $q)) return ['latest',null];
        if (preg_match('~^(?:我的?|这笔|那笔)?(?:订单|单子)'.$ship.'$~uD', $q)) return ['clarify',null];
        if (preg_match('~^(?:就查|查)?(?:最近|最新|最后)(?:的)?(?:那|这|一)?(?:笔|单|个)(?:订单|单)?$~uD', $q)) return ['follow_latest',null];
        if (preg_match('~^订单号[:：#]?'.$id.'$~uD', $q, $m)) return ['follow_number',$m[1]];
        if (preg_match('~^(?=[A-Za-z0-9_-]*[0-9])'.$id.'$~D', $q, $m)) return ['follow_number',$m[1]];
        return ['other',null];
    }

    public static function pending(array $history, string $conversation): bool
    {
        if (($history['conversation_id'] ?? null) !== $conversation || ($history['status'] ?? null) !== 'bot_active') return false;
        $messages = $history['messages'] ?? [];
        if (!is_array($messages) || count($messages)<2) return false;
        [$user,$bot] = array_slice($messages,-2);
        return ($bot['role'] ?? null)==='bot' && ($bot['content'] ?? null)===self::ASK
            && ($user['role'] ?? null)==='customer' && !empty($user['id'])
            && ($bot['reply_to_message_id'] ?? null)===$user['id']
            && self::intent((string)($user['content'] ?? ''))[0]==='clarify';
    }

    public static function answer(string $uid, string $text, ?callable $pending = null): ?string
    {
        [$kind,$number] = self::intent($text);
        if ($kind === 'other') return null;
        if ($kind === 'clarify') return self::ASK;
        if (strpos($kind,'follow_')===0 && (!$pending || !$pending())) return null;
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
