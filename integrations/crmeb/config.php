<?php
// Private per-instance file is outside the public document root and source tree.
use think\facade\Env;
$private = root_path() . 'runtime/omni-chat-deploy/config.json';
if (is_file($private)) {
    $data = json_decode(file_get_contents($private), true);
    // Malformed private configuration never silently falls back to enabled env.
    return is_array($data) ? $data : ['enabled' => false];
}
return [
    'enabled' => Env::get('omni_chat.enabled', false) === true,
    'origin' => Env::get('omni_chat.origin', ''),
    'upstream' => Env::get('omni_chat.upstream', ''),
    'secret' => Env::get('omni_chat.secret', ''),
    'allow_local_http' => Env::get('omni_chat.allow_local_http', false) === true,
    'storage' => 'unknown',
    'reply_mode' => 'unknown',
];
