import test from 'node:test';
import assert from 'node:assert/strict';
import {createChatAPI} from './protocol.mjs';
import {createPinnedFetch} from './host-session.mjs';

const identity={id:'alice',role:'customer',version:'abcdefghijklmnop'};
const pending={event_id:'11111111-1111-4111-8111-111111111111',content:'original message'};
const json=(value,status=200)=>new Response(JSON.stringify(value),{status});
function setup({verify=()=>json({identity}),renewStatus=200,retryStatus=200,conversation='c1'}={}){
  let current=true,invalidations=0,opens=0;
  const calls=[];
  const api=createChatAPI(createPinnedFetch({identity,version:identity.version,identityURL:'/auth/me',isCurrent:()=>current,onUnauthorized:()=>{invalidations++;current=false;},fetcher:async(path,options)=>{
    calls.push({path,...options});
    assert.equal(options.chatTokenRecovery,undefined,'internal recovery hint must not reach network');
    if(path==='/auth/me')return verify();
    if(path==='/host/chat-session'){
      opens++;
      return opens>1&&renewStatus!==200?json({},renewStatus):json({access_token:'token'+opens,conversation_id:opens>1?conversation:'c1',user:{id:'alice'}});
    }
    return json({},opens===1?401:retryStatus);
  }}));
  return {api,calls,invalidate:()=>{current=false;},stats:()=>({invalidations,opens})};
}
test('same business login renews once and retries exact original message',async()=>{
  const s=setup();await s.api.open();await s.api.send(pending);
  assert.deepEqual(s.stats(),{invalidations:0,opens:2});
  const messages=s.calls.filter(x=>x.path==='/chat/v1/messages');
  assert.equal(messages.length,2);assert.equal(messages[0].body,messages[1].body);
  assert.deepEqual(JSON.parse(messages[1].body),pending);
  assert.equal(messages[1].headers.get('Authorization'),'Bearer token2');
  const check=s.calls.find(x=>x.path==='/auth/me');
  assert.equal(new Headers(check.headers).has('Authorization'),false);
  assert.equal(check.cache,'no-store');assert.equal(check.credentials,'same-origin');
});
for(const [name,verify] of [
  ['logout',()=>json({identity:null})],
  ['account switch',()=>json({identity:{...identity,id:'bob'}})],
  ['login version change',()=>json({identity:{...identity,version:'qrstuvwxyzabcdef'}})],
  ['revoked login',()=>json({},401)],['revoked permission',()=>json({},403)],
])test(name+' cannot renew or resend',async()=>{
  const s=setup({verify});await s.api.open();await assert.rejects(s.api.send(pending));
  assert.deepEqual(s.stats(),{invalidations:1,opens:1});
  assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,1);
});
for(const [name,verify] of [
  ['unavailable',()=>json({},503)],['rate limited',()=>json({},429)],
  ['network failure',()=>{throw Error('offline');}],
  ['malformed identity',()=>json({identity:{id:'alice'}})],
])test(name+' preserves login but never permits resend',async()=>{
  const s=setup({verify});await s.api.open();await assert.rejects(s.api.send(pending),{code:'unavailable'});
  assert.deepEqual(s.stats(),{invalidations:0,opens:1});
});
test('renewal refusal invalidates and does not resend',async()=>{
  const s=setup({renewStatus:401});await s.api.open();await assert.rejects(s.api.send(pending));
  assert.deepEqual(s.stats(),{invalidations:1,opens:2});
  assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,1);
});
test('second chat refusal stops without a renewal loop',async()=>{
  const s=setup({retryStatus:401});await s.api.open();await assert.rejects(s.api.send(pending));
  assert.deepEqual(s.stats(),{invalidations:1,opens:2});
  assert.equal(s.calls.filter(x=>x.path==='/auth/me').length,1);
  assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,2);
});
test('changed conversation never receives original pending message',async()=>{
  const s=setup({conversation:'c2'});await s.api.open();await assert.rejects(s.api.send(pending),{code:'account_changed'});
  assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,1);
});
test('identity changes during verification cannot renew or invalidate successor',async()=>{
  const s=setup({verify:()=>{s.invalidate();return json({identity});}});
  await s.api.open();await assert.rejects(s.api.send(pending));
  assert.deepEqual(s.stats(),{invalidations:0,opens:1});
});
test('aborted verification cannot renew or resend',async()=>{
  const controller=new AbortController();
  const s=setup({verify:()=>{controller.abort();return json({identity});}});
  await s.api.open();await assert.rejects(s.api.send(pending,controller.signal),{name:'AbortError'});
  assert.deepEqual(s.stats(),{invalidations:0,opens:1});
});
test('history recovery does not send any message',async()=>{
  const s=setup();await s.api.open();await s.api.history();
  assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages'&&x.method==='POST').length,0);
});
