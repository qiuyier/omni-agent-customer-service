<?php
declare(strict_types=1);
namespace app\api\controller;

use app\Request;
use app\services\user\UserAuthServices;
use crmeb\services\CacheService;
use think\facade\Config;

final class OmniChat
{
    public function session(Request $request) { return $this->dispatch($request, 'session'); }
    public function messages(Request $request) { return $this->dispatch($request, 'messages'); }
    public function handoff(Request $request) { return $this->dispatch($request, 'handoff'); }
    public function consultations(Request $request) { return $this->dispatch($request, 'consultations'); }
    public function deskMe(Request $request) { return $this->desk($request, 'me'); }
    public function deskQueue(Request $request) { return $this->desk($request, 'queue'); }
    public function deskAction(Request $request) { return $this->desk($request, 'action'); }

    private function desk(Request $request, string $action)
    {
        require_once root_path() . 'extend/omni_chat/DeskBridge.php';
        $config = Config::get('omni_chat', []);
        $bridge = new \OmniChat\DeskBridge($config, static function (string $token): array {
            // Reject a different login realm before CRMEB's parser can delete
            // that realm's token cache while looking up the wrong account table.
            $cached = CacheService::get(md5($token), null);
            if (!is_array($cached) || ($cached['type'] ?? '') !== 'kefu') throw new \RuntimeException('identity');
            $staff = app()->make(\app\services\kefu\LoginServices::class)->parseToken($token);
            if (($staff['type'] ?? '') !== 'kefu') throw new \RuntimeException('identity');
            // Original parseToken does not recheck the current enabled flag.
            $current = \app\model\service\StoreService::where('id', (int)$staff['id'])->find();
            if (!$current || !(int)$current['status']) throw new \RuntimeException('disabled');
            return ['id' => (string)$current['id'], 'name' => (string)$current['nickname']];
        });
        $uri = (string)$request->server('REQUEST_URI', '');
        $query = $uri !== '' ? (parse_url($uri, PHP_URL_QUERY) ?? '') : $request->server('QUERY_STRING', '');
        [$code, $data] = $bridge->handle(strtoupper($request->method()), $action, $request->header(), $request->getContent(), $query === false ? 'invalid' : (string)$query);
        return json($data, $code)->header(['Cache-Control' => 'no-store', 'X-Content-Type-Options' => 'nosniff', 'Referrer-Policy' => 'no-referrer']);
    }

    private function dispatch(Request $request, string $action)
    {
        require_once root_path() . 'extend/omni_chat/Bridge.php';
        $config = Config::get('omni_chat', []);
        $config['lock_dir'] = runtime_path() . 'omni-chat-locks';
        $bridge = new \OmniChat\Bridge($config, static function (string $token): string {
            $cached = CacheService::get(md5($token), null);
            if (!is_array($cached) || ($cached['type'] ?? '') !== 'api') throw new \RuntimeException('identity');
            $auth = app()->make(UserAuthServices::class)->parseToken($token);
            // Customer tokens only: never turn an admin/kefu/application token into a customer.
            if (($auth['tokenData']['type'] ?? '') !== 'api') throw new \RuntimeException('identity');
            return (string)$auth['user']->uid;
        }, static function (string $key) { return CacheService::get($key, null); },
        static function (string $key, array $value, int $ttl): bool { return (bool)CacheService::set($key, $value, $ttl); });
        // Nginx may internally add ?s=/api/... for ThinkPHP routing. Validate
        // the original browser URI, not that server-generated routing query.
        $uri = (string)$request->server('REQUEST_URI', '');
        $query = $uri !== '' ? (parse_url($uri, PHP_URL_QUERY) ?? '') : $request->server('QUERY_STRING', '');
        [$code, $data] = $bridge->handle(strtoupper($request->method()), $action,
            $request->header(), $request->getContent(), $query === false ? 'invalid' : (string)$query);
        if ($action === 'session' && $code === 200) {
            $data['storage'] = in_array($config['storage'] ?? '', ['postgres', 'memory'], true) ? $config['storage'] : 'unknown';
            $data['reply_mode'] = in_array($config['reply_mode'] ?? '', ['fake', 'ai'], true) ? $config['reply_mode'] : 'unknown';
            $data['human_enabled'] = ($config['human_enabled'] ?? false) === true;
            $data['new_consultation_enabled'] = ($config['human_enabled'] ?? false) === true;
        }
        return json($data, $code)->header(['Cache-Control' => 'no-store',
            'X-Content-Type-Options' => 'nosniff', 'Referrer-Policy' => 'no-referrer']);
    }
}
