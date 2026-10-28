import test from 'node:test';
import assert from 'node:assert/strict';
import {hostIdentityFailureStatus,hostSessionStatusView} from './host-session-status.mjs';
import {requestJSON,APIError} from './protocol.mjs';
import {readHostIdentity} from './host-session.mjs';
test('only confirmed HTTP authentication failures request login, even if browser reports offline',()=>{
 for(const status of [401,403])for(const online of [true,false])assert.equal(hostIdentityFailureStatus(new APIError('unavailable',status),online),'unauthorized');
});
test('network, timeout, server, rate limit and malformed identity are not called expired',()=>{
 for(const error of [new TypeError('secret-url'),new Error('invalid host identity'),new APIError('unavailable'),new APIError('unavailable',503),new APIError('rate_limited',429),new APIError('unauthorized',500)]){
  assert.equal(hostIdentityFailureStatus(error,true),'unavailable');assert.equal(hostIdentityFailureStatus(error,false),'offline');
 }
});
test('retryable and checking messages never expose errors or claim login expiration',()=>{
 for(const status of ['checking','offline','unavailable'])assert.doesNotMatch(hostSessionStatusView(status).message,/登录已失效|secret-url/);
 for(const status of ['offline','unavailable'])assert.equal(hostSessionStatusView(status).retry,true);
 for(const status of ['checking','unauthorized','signingOut','logoutFailed'])assert.equal(hostSessionStatusView(status).retry,false);
 assert.equal(hostSessionStatusView('unknown'),hostSessionStatusView('unavailable'));
});
test('real JSON request errors preserve HTTP auth classification and mask transport errors',async()=>{
 for(const status of [401,403,429,503]){
  await assert.rejects(requestJSON('/identity',{fetcher:async()=>new Response('<html>unavailable</html>',{status})}),e=>{
   assert.equal(hostIdentityFailureStatus(e),[401,403].includes(status)?'unauthorized':'unavailable');return true;
  });
 }
 await assert.rejects(requestJSON('/identity',{fetcher:async()=>{throw Error('private');}}),e=>hostIdentityFailureStatus(e)==='unavailable');
});
test('missing identity is explicit logout; malformed and wrong-role identity remain fail-closed',()=>{
 assert.equal(readHostIdentity({identity:null},'customer'),null);
 for(const value of [{},{identity:{}},{identity:{id:'user',version:'a'.repeat(32),role:'staff'}}]){
  assert.throws(()=>readHostIdentity(value,'customer'),e=>hostIdentityFailureStatus(e)==='unavailable');
 }
});
