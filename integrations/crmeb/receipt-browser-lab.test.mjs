import test from 'node:test';
import assert from 'node:assert/strict';
import {allowedRoute,shouldFail} from './receipt-browser-lab.mjs';
test('proxy exposes exact test routes only',()=>{
 assert.equal(allowedRoute('POST','/api/omni-chat/message-receipt'),true);
 for(const [m,p]of [['GET','/api/omni-chat/message-receipt'],['POST','/api/omni-chat/message-receipt?uid=4'],['GET','/internal/chat/sessions'],['GET','http://evil.test'],['GET','/omni-chat-test/../config.json'],['GET','/api/admin']])assert.equal(allowedRoute(m,p),false);
 assert.equal(allowedRoute('GET','/omni-chat-test/app.js?v=1234567890abcdef'),true);
});
test('fault affects only armed successful operation type',()=>{
 assert.equal(shouldFail('send','POST','/api/omni-chat/messages',200),true);
 assert.equal(shouldFail('send','GET','/api/omni-chat/messages',200),false);
 assert.equal(shouldFail('send','POST','/api/omni-chat/messages',401),false);
 assert.equal(shouldFail('query','POST','/api/omni-chat/messages',200),false);
 assert.equal(shouldFail('query','POST','/api/omni-chat/message-receipt',200),true);
});
