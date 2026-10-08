<?php
// Private CLI acceptance only. Credentials arrive on stdin and are never printed.
declare(strict_types=1);
$credentials = json_decode(stream_get_contents(STDIN), true, 16, JSON_THROW_ON_ERROR);
$root = '/www/CRMEB/crmeb/';
require $root . 'vendor/autoload.php';
$app = new \think\App($root);
$app->initialize();
$tokens = []; $checks = []; $process = null; $secret = bin2hex(random_bytes(24)); $stage = 'setup';
function verify($ok, string $name) { global $checks; if (!$ok) throw new RuntimeException($name); $checks[] = $name; }
function requestFor(string $method, string $login, string $chat = '', string $body = '{}', array $extra = []): \app\Request {
    $r = new \app\Request();
    $r->withServer(['REQUEST_METHOD' => $method, 'QUERY_STRING' => '', 'HTTP_HOST' => 'demo.crmeb.com']);
    $r->withHeader(array_merge(['host' => 'demo.crmeb.com', 'origin' => 'http://demo.crmeb.com',
        'content-type' => 'application/json', 'authori-zation' => 'Bearer ' . $login, 'x-omni-chat-token' => $chat], $extra));
    $r->withInput($method === 'GET' ? '' : $body);
    return $r;
}
try {
    $stage = 'gateway';
    if (is_file(__DIR__ . '/ready.json')) unlink(__DIR__ . '/ready.json');
    file_put_contents(__DIR__ . '/secret', $secret); chmod(__DIR__ . '/secret', 0600);
    $process = proc_open([__DIR__ . '/gateway', __DIR__ . '/secret', __DIR__ . '/ready.json'],
        [0 => ['file', '/dev/null', 'r'], 1 => ['file', __DIR__ . '/gateway.log', 'a'], 2 => ['file', __DIR__ . '/gateway.log', 'a']], $pipes);
    for ($i = 0; $i < 100 && !is_file(__DIR__ . '/ready.json'); ++$i) usleep(50000);
    if (!is_file(__DIR__ . '/ready.json')) throw new RuntimeException('gateway startup');
    $ready = json_decode(file_get_contents(__DIR__ . '/ready.json'), true);
    $probe = curl_init($ready['origin'] . '/internal/chat/sessions');
    curl_setopt_array($probe, [CURLOPT_RETURNTRANSFER => true, CURLOPT_POST => true,
        CURLOPT_PROXY => '', CURLOPT_TIMEOUT => 5, CURLOPT_HTTPHEADER => ['Authorization: Bearer ' . $secret, 'Content-Type: application/json'],
        CURLOPT_POSTFIELDS => '{"customer_id":"probe"}']);
    $probeData = curl_exec($probe);
    $probeResult = ['status' => curl_getinfo($probe, CURLINFO_RESPONSE_CODE), 'type' => curl_getinfo($probe, CURLINFO_CONTENT_TYPE),
        'errno' => curl_errno($probe), 'keys' => array_keys(json_decode($probeData ?: '{}', true) ?: [])];
    $decodedProbe = json_decode($probeData ?: '{}', true);
    $probeResult['token_valid'] = preg_match('/\Achat_[A-Za-z0-9_-]{43}\z/D', $decodedProbe['access_token'] ?? '') === 1;
    $probeResult['conversation_valid'] = preg_match('/\Awbc_[a-f0-9]{64}\z/D', $decodedProbe['conversation_id'] ?? '') === 1;
    $probeResult['expiry_seconds'] = strtotime($decodedProbe['expires_at'] ?? '') - time();
    curl_close($probe);
    \think\facade\Config::set(['enabled' => true, 'origin' => 'http://demo.crmeb.com',
        'allow_local_http' => true, 'upstream' => $ready['origin'], 'secret' => $secret], 'omni_chat');
    require_once $root . 'extend/omni_chat/Bridge.php';
    $debugBridge = new \OmniChat\Bridge(\think\facade\Config::get('omni_chat'), static function () { return '3'; }, static function () { return null; }, static function () { return true; });
    $debugSend = new ReflectionMethod($debugBridge, 'send'); $debugSend->setAccessible(true);
    [$debugCode, $debugData] = $debugSend->invoke($debugBridge, 'POST', '/internal/chat/sessions', $secret, ['customer_id' => 'probe2']);
    $probeResult['bridge_send_status'] = $debugCode;
    $probeResult['bridge_send_keys'] = array_keys($debugData);
    $stage = 'login';
    foreach ($credentials as $c) {
        $result = app()->make(\app\services\user\LoginServices::class)->login($c['account'], $c['password'], 0, 0);
        $tokens[] = $result['token'];
    }
    unset($credentials, $c, $result);
    $stage = 'controller';
    $controller = new \app\api\controller\OmniChat();
    $response = $controller->session(requestFor('POST', $tokens[0]));
    $stage = 'user_a_session_http_' . $response->getCode();
    verify($response->getCode() === 200, 'user_a_session'); $a = $response->getData();
    $response = $controller->session(requestFor('POST', $tokens[1]));
    verify($response->getCode() === 200, 'user_b_session'); $b = $response->getData();
    verify($a['conversation_id'] !== $b['conversation_id'], 'distinct_conversations');
    $again = $controller->session(requestFor('POST', $tokens[0]));
    verify($again->getCode() === 200 && $again->getData() === $a, 'refresh_reuses_session');
    foreach ([[0, $a, 'identity-alpha'], [1, $b, 'identity-beta']] as [$idx, $session, $message]) {
        $sent = $controller->messages(requestFor('POST', $tokens[$idx], $session['access_token'], json_encode(['event_id' => 'same-test-event', 'content' => $message])));
        verify($sent->getCode() === 200, 'user_' . $idx . '_send');
        $history = $controller->messages(requestFor('GET', $tokens[$idx], $session['access_token']));
        verify($history->getCode() === 200, 'user_' . $idx . '_history');
        $data = $history->getData();
        verify(count($data['messages']) === 2 && $data['messages'][0]['content'] === $message, 'user_' . $idx . '_own_history_only');
    }
    verify($controller->messages(requestFor('GET', $tokens[1], $a['access_token']))->getCode() === 401, 'cross_user_capability_rejected');
    verify($controller->session(requestFor('POST', $tokens[0], '', '{"customer_id":"4"}'))->getCode() === 400, 'forged_identity_rejected');
    verify($controller->session(requestFor('POST', 'invalid'))->getCode() === 401, 'invalid_login_rejected');
    verify($controller->session(requestFor('POST', $tokens[0], '', '{}', ['origin' => 'http://other.test']))->getCode() === 403, 'foreign_origin_rejected');
    $stage = 'logout';
    $loginController = app()->make(\app\api\controller\v1\LoginController::class);
    $loginController->logout(requestFor('GET', $tokens[0]));
    verify(\crmeb\services\CacheService::get(md5($tokens[0]), null) === null, 'native_logout_deleted_login');
    verify($controller->messages(requestFor('GET', $tokens[0], $a['access_token']))->getCode() === 401, 'logged_out_history_rejected');
    verify($controller->messages(requestFor('POST', $tokens[0], $a['access_token'], '{"event_id":"after-logout","content":"test"}'))->getCode() === 401, 'logged_out_send_rejected');
    verify($controller->session(requestFor('POST', $tokens[0]))->getCode() === 401, 'logged_out_exchange_rejected');
    verify($controller->messages(requestFor('GET', $tokens[1], $b['access_token']))->getCode() === 200, 'other_user_still_valid');
    $report = ['passed' => true, 'checks' => $checks, 'external_model_calls' => 0,
        'scope' => 'real CRMEB login/auth/cache/controller + real loopback memory gateway; not enabled public routing or UI'];
} catch (\Throwable $e) {
    // No vendor exception text: it can contain configuration, SQL or credentials.
    $report = ['passed' => false, 'stage' => $stage, 'checks' => $checks, 'error_class' => get_class($e), 'line' => $e->getLine()];
    $report['gateway_probe'] = $probeResult ?? null;
    if (count($tokens)) {
        try { $auth = app()->make(\app\services\user\UserAuthServices::class)->parseToken($tokens[0]);
            $report['auth_probe'] = ['uid' => (int)$auth['user']->uid, 'type' => $auth['tokenData']['type'] ?? null];
        } catch (\Throwable $ignored) { $report['auth_probe'] = get_class($ignored); }
    }
} finally {
    foreach ($tokens as $token) {
        \crmeb\services\CacheService::delete(md5($token));
        \crmeb\services\CacheService::delete('omni-chat:v1:' . hash_hmac('sha256', $token, $secret));
    }
    if (is_resource($process)) { proc_terminate($process); proc_close($process); }
    if (is_file(__DIR__ . '/secret')) unlink(__DIR__ . '/secret');
}
file_put_contents(__DIR__ . '/report.json', json_encode($report, JSON_UNESCAPED_UNICODE | JSON_PRETTY_PRINT));
echo json_encode($report, JSON_UNESCAPED_UNICODE) . PHP_EOL;
exit($report['passed'] ? 0 : 1);
