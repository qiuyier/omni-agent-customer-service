<?php
declare(strict_types=1);
namespace OmniChat;
require_once __DIR__ . '/Transport.php';

/** CRMEB staff identity never comes from a browser-supplied agent ID. */
final class DeskBridge
{
    private array $config;
    private $authenticate;
    private $transport;
    public function __construct(array $config, callable $authenticate, ?callable $transport = null)
    { $this->config = $config; $this->authenticate = $authenticate; $this->transport = $transport; }

    private function error(int $code): array { return [$code, ['error' => $code === 401 ? 'authentication_required' : 'request_rejected']]; }
    private function actor(string $token): ?array
    {
        try {
            $user = ($this->authenticate)($token);
            if (!is_array($user) || !is_string($user['id'] ?? null) || !preg_match('/\A[1-9][0-9]{0,19}\z/D', $user['id'])) return null;
            $agent = $this->config['agents'][$user['id']] ?? null;
            if (!is_array($agent) || !is_string($agent['id'] ?? null) || !preg_match('/\A[A-Za-z0-9_-]{1,128}\z/D', $agent['id']) ||
                !is_string($agent['secret'] ?? null) || !preg_match('/\A[A-Za-z0-9_-]{32,128}\z/D', $agent['secret']) ||
                strpos($agent['secret'], 'chat_') === 0 || $agent['secret'] === ($this->config['secret'] ?? '')) return null;
            return ['uid' => $user['id'], 'id' => $agent['id'], 'secret' => $agent['secret'],
                'name' => is_string($user['name'] ?? null) ? mb_substr($user['name'], 0, 80) : '客服'];
        } catch (\Throwable $e) { return null; }
    }

    public function handle(string $method, string $action, array $headers, string $body, string $query = ''): array
    {
        try {
            if (($this->config['enabled'] ?? false) !== true || ($this->config['human_enabled'] ?? false) !== true) return $this->error(404);
            $origin = $this->config['origin'] ?? ''; $url = parse_url($origin);
            if (!$url || !isset($url['scheme'], $url['host']) || isset($url['user']) || isset($url['pass']) || isset($url['path']) || isset($url['query']) || isset($url['fragment']) ||
                !in_array($url['scheme'], ['http', 'https'], true) || ($url['scheme'] === 'http' && ($this->config['allow_local_http'] ?? false) !== true) ||
                !preg_match('/\Ahttp:\/\/127\.0\.0\.1:([0-9]{1,5})\z/D', $this->config['upstream'] ?? '', $match) || (int)$match[1] < 1 || (int)$match[1] > 65535) return $this->error(503);
            $h = array_change_key_case($headers, CASE_LOWER);
            $host = $url['host'] . (isset($url['port']) ? ':' . $url['port'] : '');
            if (($h['host'] ?? '') !== $host || ($h['sec-fetch-site'] ?? '') === 'cross-site' || (isset($h['origin']) && $h['origin'] !== $origin) ||
                ($method === 'POST' && ($h['origin'] ?? '') !== $origin)) return $this->error(403);
            if ($query !== '' || !in_array($action, ['me', 'queue', 'action'], true)) return $this->error(400);
            if ($method !== ($action === 'action' ? 'POST' : 'GET')) return $this->error(405);
            if (strlen($body) > 32768) return $this->error(413);
            if ($method === 'GET' && $body !== '') return $this->error(400);
            if ($method === 'POST' && strtolower(trim(explode(';', $h['content-type'] ?? '')[0])) !== 'application/json') return $this->error(415);
            if (isset($h['authorization']) || isset($h['x-omni-chat-token']) || !preg_match('/\ABearer ([A-Za-z0-9_.-]{20,4096})\z/D', $h['authori-zation'] ?? '', $token)) return $this->error(401);
            $actor = $this->actor($token[1]); if (!$actor) return $this->error(401);
            if ($action === 'me') return [200, ['agent' => ['id' => $actor['id'], 'name' => $actor['name']], 'accounts' => []]];
            $input = null;
            if ($action === 'action') {
                $obj = json_decode($body, false, 16, JSON_THROW_ON_ERROR);
                if (!$obj instanceof \stdClass) return $this->error(400);
                $input = (array)$obj;
                if (isset($input['expected_agent_id']) && $input['expected_agent_id'] !== $actor['id']) return $this->error(403);
                unset($input['expected_agent_id']);
                $kind = $input['action'] ?? '';
                $fields = ['history' => ['before_message_id','after_message_id','from_message_id'], 'claim' => [], 'close' => [],
                    'reply' => ['event_id','content'], 'read' => ['message_ids']];
                if (!is_string($kind) || !isset($fields[$kind]) || array_diff(array_keys($input), array_merge(['conversation_id','action','revision'], $fields[$kind])) ||
                    !is_string($input['conversation_id'] ?? null) || !preg_match('/\A[A-Za-z0-9_-]{1,128}\z/D', $input['conversation_id'])) return $this->error(400);
                if (isset($input['revision']) && (!is_int($input['revision']) || $input['revision'] < 1)) return $this->error(400);
                // The gateway validates action-specific fields and checks source/ownership.
            }
            [$code, $data] = Transport::send($this->config, $method, '/internal/chat/desk/' . $action, $actor['secret'], $input, $this->transport);
            $after = $this->actor($token[1]);
            if (!$after || $after['uid'] !== $actor['uid'] || $after['id'] !== $actor['id'] || !hash_equals($after['secret'], $actor['secret'])) return $this->error(401);
            if ($code !== 200) return $this->error(in_array($code, [400,401,403,404,409,413,429,503], true) ? $code : 502);
            return [200, $data];
        } catch (\JsonException $e) { return $this->error(400); }
        catch (\Throwable $e) { return $this->error(503); }
    }
}
