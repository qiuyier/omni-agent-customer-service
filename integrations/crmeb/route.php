<?php
// Additive routes: deliberately no broad CORS middleware or existing login changes.
use think\facade\Route;
Route::post('omni-chat/session', 'OmniChat/session');
Route::get('omni-chat/messages', 'OmniChat/messages');
Route::post('omni-chat/messages', 'OmniChat/messages');
Route::post('omni-chat/message-receipt', 'OmniChat/receipt');
Route::post('omni-chat/handoff', 'OmniChat/handoff');
Route::post('omni-chat/consultations', 'OmniChat/consultations');
Route::get('omni-chat/desk/me', 'OmniChat/deskMe');
Route::get('omni-chat/desk/queue', 'OmniChat/deskQueue');
Route::post('omni-chat/desk/action', 'OmniChat/deskAction');
