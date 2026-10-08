<?php
declare(strict_types=1);
require __DIR__ . '/Bridge.php';

$cache = []; $calls = 0; $revoked = []; $hook = null;
$a = str_repeat('a', 30); $b = str_repeat('b', 30); $sameUser = str_repeat('c', 30);
$config = ['enabled' => true, 'origin' => 'http://demo.crmeb.com', 'allow_local_http' => true,
    'upstream' => 'http://127.0.0.1:18093', 'secret' => str_repeat('s', 40),
    'lock_dir' => sys_get_temp_dir() . '/omni-bridge-unit-' . getmypid()];
$auth = static function ($t) use (&$revoked, $a, $b, $sameUser) { return isset($revoked[$t]) ? null : ([$a => '3', $b => '4', $sameUser => '3'][$t] ?? null); };
$get = static function ($k) use (&$cache) { return $cache[$k] ?? null; };
$set = static function ($k, $v, $ttl) use (&$cache) { $cache[$k] = $v; return true; };
$transport = static function ($method, $path, $token, $body) use (&$calls, &$hook) {
    ++$calls;
    if ($hook) return $hook($method, $path, $token, $body);
    if ($path === '/internal/chat/sessions') return [201, [
        'access_token' => 'chat_' . str_pad((string)$calls, 43, 'a'),
        'expires_at' => gmdate('Y-m-d\TH:i:s', time() + 300) . '.123456789Z',
        'conversation_id' => 'wbc_' . hash('sha256', $body['customer_id'])]];
    return [200, ['messages' => []]];
};
$bridge = new \OmniChat\Bridge($config, $auth, $get, $set, $transport);
$headers = static function ($login, $chat = '') { return ['Host' => 'demo.crmeb.com', 'Origin' => 'http://demo.crmeb.com',
    'Content-Type' => 'application/json', 'Authori-zation' => 'Bearer ' . $login, 'X-Omni-Chat-Token' => $chat]; };
$passed = 0;
function check($condition, $name) { global $passed; if (!$condition) throw new RuntimeException($name); ++$passed; }
try {
    [$code, $s1] = $bridge->handle('POST', 'session', $headers($a), '{}'); check($code === 200, 'first session');
    $before = $calls;
    [$code, $again] = $bridge->handle('POST', 'session', $headers($a), '{}'); check($code === 200 && $again === $s1 && $calls === $before, 'refresh reuse');
    [$code, $s2] = $bridge->handle('POST', 'session', $headers($b), '{}'); check($code === 200 && $s1['conversation_id'] !== $s2['conversation_id'], 'user isolation');
    check($bridge->handle('GET', 'messages', $headers($b, $s1['access_token']), '')[0] === 401, 'foreign capability');
    check($bridge->handle('GET', 'messages', $headers($sameUser, $s1['access_token']), '')[0] === 401, 'same uid different login');
    check($bridge->handle('GET', 'messages', $headers($a, $s1['access_token']), '')[0] === 200, 'owner access');
    $key = 'omni-chat:v1:' . hash_hmac('sha256', $a, $config['secret']);
    $saved = $cache[$key]; $cache[$key]['expiry'] = time() - 1;
    check($bridge->handle('GET', 'messages', $headers($a, $s1['access_token']), '')[0] === 401, 'expired binding');
    $cache[$key] = $saved;
    $lock = fopen($config['lock_dir'] . '/' . (hexdec(substr(hash('sha256', $key), 0, 2)) % 64) . '.lock', 'c');
    flock($lock, LOCK_EX | LOCK_NB);
    check($bridge->handle('POST', 'session', $headers($a), '{}')[0] === 429, 'concurrent mint busy');
    flock($lock, LOCK_UN); fclose($lock);
    foreach (['{"customer_id":"4"}', '{"conversation_id":"fake"}', '[]', 'null', '{'] as $body) check($bridge->handle('POST', 'session', $headers($a), $body)[0] === 400, 'untrusted identity/body');
    $h = $headers($a); $h['Origin'] = 'http://evil.test'; check($bridge->handle('POST', 'session', $h, '{}')[0] === 403, 'origin');
    $h = $headers($a); unset($h['Origin']); check($bridge->handle('POST', 'session', $h, '{}')[0] === 403, 'missing origin');
    $h = $headers($a); $h['Host'] = 'evil.test'; check($bridge->handle('POST', 'session', $h, '{}')[0] === 403, 'host');
    $h = $headers($a); $h['Authorization'] = 'Bearer other'; check($bridge->handle('POST', 'session', $h, '{}')[0] === 401, 'ambiguous credential');
    check($bridge->handle('POST', 'session', $headers('invalid'), '{}')[0] === 401, 'invalid token');
    check($bridge->handle('GET', 'messages', $headers($a, $s1['access_token']), '', 'uid=4')[0] === 400, 'query identity');
    check($bridge->handle('POST', 'messages', $headers($a, $s1['access_token']), '{"event_id":"x","content":"test","role":"human_agent"}')[0] === 400, 'role injection');
    $hook = static function () use (&$revoked, $a) { $revoked[$a] = true; return [200, ['messages' => ['private']]]; };
    check($bridge->handle('GET', 'messages', $headers($a, $s1['access_token']), '')[0] === 401, 'logout during request');
    $before = $calls;
    check($bridge->handle('GET', 'messages', $headers($a, $s1['access_token']), '')[0] === 401 && $before === $calls, 'logout before request');
    $hook = static function () { return [302, ['secret' => 'do not disclose']]; };
    $error = $bridge->handle('GET', 'messages', $headers($b, $s2['access_token']), '');
    check($error[0] === 502 && strpos(json_encode($error), 'disclose') === false, 'sanitized upstream redirect');
    $off = new \OmniChat\Bridge([], $auth, $get, $set, $transport);
    check($off->handle('POST', 'session', $headers($b), '{}')[0] === 404, 'disabled');
    $bad = $config; $bad['upstream'] = 'http://example.com';
    check((new \OmniChat\Bridge($bad, $auth, $get, $set, $transport))->handle('POST', 'session', $headers($b), '{}')[0] === 503, 'upstream boundary');
    $cache = []; $hook = null;
    [$code, $beforeRestart] = $bridge->handle('POST', 'session', $headers($b), '{}');
    check($code === 200, 'pre restart session');
    $hook = static function () { return [401, ['error' => 'unauthorized']]; };
    check($bridge->handle('GET', 'messages', $headers($b, $beforeRestart['access_token']), '')[0] === 401, 'restart invalid capability');
    $hook = null;
    [$code, $afterRestart] = $bridge->handle('POST', 'session', $headers($b), '{}');
    check($code === 200 && $afterRestart['access_token'] !== $beforeRestart['access_token'] && $afterRestart['conversation_id'] === $beforeRestart['conversation_id'], 'restart remints stable conversation');
    $cache = [];
    $noCache = new \OmniChat\Bridge($config, $auth, $get, static function () { return false; }, $transport);
    check($noCache->handle('POST', 'session', $headers($b), '{}')[0] === 503, 'cache failure closed');
    $hook = static function () use (&$revoked, $b, $s2) { $revoked[$b] = true; return [201, $s2]; };
    check($bridge->handle('POST', 'session', $headers($b), '{}')[0] === 401, 'logout during mint');
    echo json_encode(['passed' => $passed]) . PHP_EOL;
} finally {
    // Only our per-process temporary lock files; no recursive directory deletion.
    foreach (glob($config['lock_dir'] . '/*.lock') ?: [] as $file) unlink($file);
    if (is_dir($config['lock_dir'])) rmdir($config['lock_dir']);
}
