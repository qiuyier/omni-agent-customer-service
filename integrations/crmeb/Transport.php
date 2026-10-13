<?php
declare(strict_types=1);
namespace OmniChat;

// Private, fixed-origin HTTP transport shared by customer/staff adapters.
final class Transport
{
    public static function send(array $config, string $method, string $path, string $token, ?array $body, ?callable $transport = null): array
    {
        if ($transport !== null) return ($transport)($method, $path, $token, $body);
        $ch = curl_init($config['upstream'] . $path);
        $raw = '';
        try {
            curl_setopt_array($ch, [CURLOPT_CUSTOMREQUEST => $method, CURLOPT_HTTPHEADER => [
                'Authorization: Bearer ' . $token, 'Content-Type: application/json', 'Accept: application/json'],
                CURLOPT_FOLLOWLOCATION => false, CURLOPT_PROXY => '', CURLOPT_CONNECTTIMEOUT => 2,
                CURLOPT_TIMEOUT => 120, CURLOPT_PROTOCOLS => CURLPROTO_HTTP,
                CURLOPT_WRITEFUNCTION => static function ($ch, string $chunk) use (&$raw): int {
                    if (strlen($raw) + strlen($chunk) > 1048576) return 0;
                    $raw .= $chunk; return strlen($chunk);
                }]);
            if ($body !== null) curl_setopt($ch, CURLOPT_POSTFIELDS, json_encode((object)$body, JSON_THROW_ON_ERROR));
            if (curl_exec($ch) === false) throw new \RuntimeException('upstream');
            $type = strtolower(trim(explode(';', curl_getinfo($ch, CURLINFO_CONTENT_TYPE) ?: '')[0]));
            $data = json_decode($raw, true, 64, JSON_THROW_ON_ERROR);
            if ($type !== 'application/json' || !is_array($data)) throw new \RuntimeException('upstream');
            return [(int)curl_getinfo($ch, CURLINFO_RESPONSE_CODE), $data];
        } catch (\Throwable $e) { return [502, []]; }
        finally { curl_close($ch); }
    }

}
