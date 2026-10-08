<?php
declare(strict_types=1);

namespace OmniChat;

/** Host-side adapter only. Never put CRMEB credentials into the chat gateway. */
final class Bridge
{
    private array $config;
    private $authenticate;
    private $get;
    private $set;
    private $transport;

    public function __construct(array $config, callable $authenticate, callable $get, callable $set, ?callable $transport = null)
    {
        $this->config = $config;
        $this->authenticate = $authenticate;
        $this->get = $get;
        $this->set = $set;
        $this->transport = $transport;
    }

    /** Returns [HTTP status, JSON object]. No upstream diagnostic or credential errors escape. */
    public function handle(string $method, string $action, array $headers, string $body, string $query = ''): array
    {
        try {
            if (($this->config['enabled'] ?? false) !== true) return $this->error(404);
            $origin = $this->config['origin'] ?? '';
            $url = parse_url($origin);
            $secret = $this->config['secret'] ?? '';
            if (!$url || !isset($url['host'], $url['scheme']) || isset($url['user']) || isset($url['pass']) ||
                isset($url['path']) || isset($url['query']) || isset($url['fragment']) ||
                !in_array($url['scheme'], ['https', 'http'], true) ||
                ($url['scheme'] === 'http' && ($this->config['allow_local_http'] ?? false) !== true) ||
                !preg_match('/\A[A-Za-z0-9_-]{32,128}\z/D', $secret) || strpos($secret, 'chat_') === 0 ||
                !preg_match('/\Ahttp:\/\/127\.0\.0\.1:([0-9]{1,5})\z/D', $this->config['upstream'] ?? '', $match) ||
                (int)$match[1] < 1 || (int)$match[1] > 65535) return $this->error(503);
            $h = array_change_key_case($headers, CASE_LOWER);
            $host = $url['host'] . (isset($url['port']) ? ':' . $url['port'] : '');
            if (($h['host'] ?? '') !== $host || ($h['sec-fetch-site'] ?? '') === 'cross-site' ||
                (isset($h['origin']) && $h['origin'] !== $origin) ||
                ($method === 'POST' && ($h['origin'] ?? '') !== $origin)) return $this->error(403);
            if ($query !== '' || !in_array($action, ['session', 'messages'], true)) return $this->error(400);
            if (($action === 'session' && $method !== 'POST') || !in_array($method, ['GET', 'POST'], true)) return $this->error(405);
            if (strlen($body) > 32768) return $this->error(413);
            if ($method === 'POST' && strtolower(trim(explode(';', $h['content-type'] ?? '')[0])) !== 'application/json') return $this->error(415);
            $input = $method === 'POST' ? json_decode($body, false, 16, JSON_THROW_ON_ERROR) : new \stdClass();
            if (!$input instanceof \stdClass || ($method === 'GET' && $body !== '')) return $this->error(400);
            $input = (array)$input;
            $allowed = $action === 'session' ? [] : ['event_id', 'content'];
            if (array_diff(array_keys($input), $allowed)) return $this->error(400);
            if ($action === 'messages' && $method === 'POST' &&
                (count($input) !== 2 || !is_string($input['event_id'] ?? null) || !is_string($input['content'] ?? null) ||
                 !preg_match('/\A[A-Za-z0-9_-]{1,128}\z/D', $input['event_id']) || trim($input['content']) === '' || strlen($input['content']) > 4000)) return $this->error(400);
            // CRMEB's actual H5 header, kept separate from the chat capability.
            if (isset($h['authorization']) || !preg_match('/\ABearer ([A-Za-z0-9_.-]{20,4096})\z/D', $h['authori-zation'] ?? '', $m)) return $this->error(401);
            $login = $m[1];
            $uid = $this->user($login);
            if ($uid === null) return $this->error(401);
            $key = 'omni-chat:v1:' . hash_hmac('sha256', $login, $secret);
            if ($action === 'session') return $this->session($login, $uid, $key, $secret);
            $token = $h['x-omni-chat-token'] ?? '';
            $binding = ($this->get)($key);
            if (!$this->bound($binding, $uid, $token)) return $this->error(401);
            [$code, $data] = $this->send($method, '/chat/v1/messages', $token, $method === 'POST' ? $input : null);
            // Logout during upstream execution must not return history/replies.
            if ($this->user($login) !== $uid || !$this->bound(($this->get)($key), $uid, $token)) return $this->error(401);
            if ($code === 401) {
                // Gateway restart invalidates its in-memory capability. Do not
                // return that same cached token on the browser's next exchange.
                if (!(($this->set)($key, [], 1))) return $this->error(503);
                return $this->error(401);
            }
            if (!in_array($code, [200, 201], true)) return $this->error(in_array($code, [400, 401, 403, 409, 413, 429, 503], true) ? $code : 502);
            return [$code, $data];
        } catch (\JsonException $e) {
            return $this->error(400);
        } catch (\Throwable $e) {
            return $this->error(503);
        }
    }

    private function user(string $token): ?string
    {
        try {
            $uid = ($this->authenticate)($token);
            return is_string($uid) && preg_match('/\A[1-9][0-9]{0,19}\z/D', $uid) ? $uid : null;
        } catch (\Throwable $e) { return null; }
    }

    private function bound($binding, string $uid, string $token): bool
    {
        return is_array($binding) && ($binding['uid'] ?? null) === $uid &&
            is_int($binding['expiry'] ?? null) && $binding['expiry'] > time() &&
            preg_match('/\Achat_[A-Za-z0-9_-]{43}\z/D', $token) === 1 &&
            is_string($binding['session']['access_token'] ?? null) && hash_equals($binding['session']['access_token'], $token);
    }

    private function session(string $login, string $uid, string $key, string $secret): array
    {
        // Fixed-size lock stripes prevent unbounded files and concurrent mint storms.
        $dir = $this->config['lock_dir'] ?? '';
        if ($dir === '' || (!is_dir($dir) && !mkdir($dir, 0700, true) && !is_dir($dir))) return $this->error(503);
        $lock = fopen($dir . '/' . (hexdec(substr(hash('sha256', $key), 0, 2)) % 64) . '.lock', 'c');
        if (!$lock) return $this->error(503);
        try {
            if (!flock($lock, LOCK_EX | LOCK_NB)) return $this->error(429);
            $b = ($this->get)($key);
            if ($this->bound($b, $uid, $b['session']['access_token'] ?? '') && $b['expiry'] > time() + 30) {
                if ($this->user($login) !== $uid) return $this->error(401);
                return [200, $b['session']];
            }
            [$code, $data] = $this->send('POST', '/internal/chat/sessions', $secret, ['customer_id' => $uid]);
            // Go emits RFC3339Nano; PHP 7.4 strtotime rejects 7..9 fractional digits.
            // Drop only a validated fraction, rounding expiry DOWN, never extending it.
            $expiry = false;
            if (is_string($data['expires_at'] ?? null) && preg_match('/\A([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(?:\.[0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})\z/D', $data['expires_at'], $timeParts)) {
                $expiry = strtotime($timeParts[1] . $timeParts[2]);
            }
            if ($code !== 201 || array_diff(array_keys($data), ['access_token', 'expires_at', 'conversation_id']) ||
                !is_string($data['access_token'] ?? null) || !preg_match('/\Achat_[A-Za-z0-9_-]{43}\z/D', $data['access_token']) ||
                !is_string($data['conversation_id'] ?? null) || !preg_match('/\Awbc_[a-f0-9]{64}\z/D', $data['conversation_id']) ||
                $expiry === false || $expiry <= time() || $expiry > time() + 900) return $this->error(502);
            if ($this->user($login) !== $uid) return $this->error(401);
            $ttl = $expiry - time();
            if ($ttl <= 0) return $this->error(502);
            if (!(($this->set)($key, ['uid' => $uid, 'expiry' => $expiry, 'session' => $data], $ttl))) return $this->error(503);
            return [200, $data];
        } finally {
            flock($lock, LOCK_UN);
            fclose($lock);
        }
    }

    private function send(string $method, string $path, string $token, ?array $body): array
    {
        if ($this->transport !== null) return ($this->transport)($method, $path, $token, $body);
        $ch = curl_init($this->config['upstream'] . $path);
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

    private function error(int $status): array
    {
        return [$status, ['error' => $status === 401 ? 'authentication_required' : 'request_rejected']];
    }
}
