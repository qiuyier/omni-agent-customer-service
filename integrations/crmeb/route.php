<?php
// Additive routes: deliberately no broad CORS middleware or existing login changes.
use think\facade\Route;
Route::post('omni-chat/session', 'OmniChat/session');
Route::get('omni-chat/messages', 'OmniChat/messages');
Route::post('omni-chat/messages', 'OmniChat/messages');
