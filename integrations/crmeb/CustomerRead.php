<?php
declare(strict_types=1);
namespace OmniChat;
final class CustomerRead
{
    public static function previous(array $history,string $conversation): ?array
    {
        if (($history['conversation_id']??null)!==$conversation || ($history['status']??null)!=='bot_active') return null;
        $m=$history['messages']??null;
        if (!is_array($m)||count($m)<2) return null;
        [$q,$a]=array_slice($m,-2);
        if (($q['role']??null)!=='customer'||empty($q['id'])||($a['role']??null)!=='bot'||($a['reply_to_message_id']??null)!==$q['id']||!is_string($q['content']??null)||!is_string($a['content']??null)) return null;
        return ['question'=>$q['content'],'answer'=>$a['content']];
    }
    public static function text(array $result): ?string
    {
        $k=$result['kind']??null;
        if ($k==='pass' && count($result)===1) return null;
        $s=$result['text']??null;
        if (count($result)===2 && in_array($k,['answer','clarify','unavailable'],true) && is_string($s) && preg_match('/\S/u',$s) && strlen($s)<=1024) return $s;
        throw new \RuntimeException('invalid_host_reply');
    }
}
