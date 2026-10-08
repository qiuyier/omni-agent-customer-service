import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {startSample} from './run.mjs';
import {startHost} from './host.mjs';

function client(origin) {
  return async(path,{method='GET',body,cookie,token,headers={}}={})=>{
    const response=await fetch(origin+path,{method,headers:{...(method==='POST'?{Origin:origin,'Content-Type':'application/json'}:{}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{}),...headers},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(20000)});
    const text=await response.text();
    return {status:response.status,cookie:response.headers.get('set-cookie')?.split(';')[0],cookieHeader:response.headers.get('set-cookie'),data:text?JSON.parse(text):null};
  };
}

test('independent Node login backend and actual Go gateway', {timeout:180000}, async t=>{
  const sample=await startSample();t.after(()=>sample.stop());const call=client(sample.origin);
  for(const path of ['/','/chat/','/assets/app.js','/assets/app.css']) {
    const r=await fetch(sample.origin+path);assert.equal(r.status,200);assert.ok((await r.text()).length);
  }
  for(const path of ['/internal/chat/sessions','/v1/conversations/x/messages','/assets/../host.mjs'])assert.equal((await call(path)).status,404);
  assert.equal((await call('/host/chat-session',{method:'POST',body:{}})).status,401);
  assert.equal((await call('/demo/login',{method:'POST',body:{account:'__proto__'}})).status,400);
  assert.equal((await call('/demo/login',{method:'POST',body:{account:'lin'},headers:{Origin:'http://foreign.invalid'}})).status,403);
  assert.equal((await call('/demo/login',{method:'POST',body:{account:'lin',customer_id:'admin'}})).status,400);
  const a=await call('/demo/login',{method:'POST',body:{account:'lin'}}),b=await call('/demo/login',{method:'POST',body:{account:'zhou'}});
  assert.equal(a.status,200);assert.match(a.cookieHeader,/HttpOnly/);assert.match(a.cookieHeader,/SameSite=Strict/);
  assert.equal(a.data.user.id,'member-1001');assert.equal(b.data.user.id,'member-1002');
  const session=async cookie=>{const r=await call('/host/chat-session',{method:'POST',body:{},cookie});assert.equal(r.status,200);return r.data;};
  const sa=await session(a.cookie),sb=await session(b.cookie);
  assert.notEqual(sa.conversation_id,sb.conversation_id);
  assert.equal((await call('/host/chat-session',{method:'POST',body:{customer_id:'member-1002'},cookie:a.cookie})).status,400);
  assert.equal((await call('/chat/v1/messages',{cookie:b.cookie,token:sa.access_token})).status,401);
  assert.equal((await call('/chat/v1/messages?conversation_id='+sa.conversation_id,{cookie:b.cookie,token:sb.access_token})).status,400);
  assert.equal((await call('/chat/v1/messages',{method:'POST',body:{event_id:'a',content:'test',role:'system'},cookie:a.cookie,token:sa.access_token})).status,400);
  const event={event_id:'same-event',content:'独立业务甲的私有测试内容'};
  const first=await call('/chat/v1/messages',{method:'POST',body:event,cookie:a.cookie,token:sa.access_token});
  assert.equal(first.status,200);assert.equal(first.data.outcome,'replied');
  const replay=await call('/chat/v1/messages',{method:'POST',body:event,cookie:a.cookie,token:sa.access_token});assert.deepEqual(replay.data,first.data);
  assert.equal((await session(a.cookie)).access_token,sa.access_token);
  const history=await call('/chat/v1/messages',{cookie:a.cookie,token:sa.access_token});assert.equal(history.data.messages.length,2);
  assert.equal((await call('/chat/v1/messages',{cookie:b.cookie,token:sb.access_token})).data.messages.length,0);
  const second=await call('/chat/v1/messages',{method:'POST',body:{event_id:event.event_id,content:'独立业务乙的私有测试内容'},cookie:b.cookie,token:sb.access_token});
  assert.equal(second.status,200);assert.notEqual(second.data.message_id,first.data.message_id);
  assert.doesNotMatch(JSON.stringify((await call('/chat/v1/messages',{cookie:b.cookie,token:sb.access_token})).data),/独立业务甲/);
  assert.equal((await call('/demo/logout',{method:'POST',body:{},cookie:a.cookie})).status,204);
  assert.equal((await call('/chat/v1/messages',{cookie:a.cookie,token:sa.access_token})).status,401);
  const anew=await call('/demo/login',{method:'POST',body:{account:'lin'}}),sn=await session(anew.cookie);
  assert.equal(sn.conversation_id,sa.conversation_id);
  assert.equal((await call('/chat/v1/messages',{cookie:anew.cookie,token:sa.access_token})).status,401);
  assert.equal((await call('/chat/v1/messages',{cookie:anew.cookie,token:sn.access_token})).data.messages.length,2);
  const switched=await call('/demo/login',{method:'POST',body:{account:'zhou'},cookie:anew.cookie});
  assert.equal(switched.status,200);
  assert.equal((await call('/chat/v1/messages',{cookie:anew.cookie,token:sn.access_token})).status,401);
  const switchedSession=await session(switched.cookie);assert.equal(switchedSession.conversation_id,sb.conversation_id);
});

test('logout suppresses an in-flight history response; expired login cannot access', {timeout:30000}, async t=>{
  const secret='h'.repeat(43),token='chat_'+'t'.repeat(43),conversation='wbc_'+'a'.repeat(64);
  let clock=Date.now(),release,entered;
  const waiting=new Promise(resolve=>entered=resolve);
  const upstream=http.createServer(async(req,res)=>{
    if(req.url==='/internal/chat/sessions'){
      assert.equal(req.headers.authorization,'Bearer '+secret);
      let body='';for await(const c of req)body+=c;
      assert.deepEqual(JSON.parse(body),{customer_id:'member-1001'});
      res.writeHead(201,{'Content-Type':'application/json'});res.end(JSON.stringify({access_token:token,expires_at:new Date(clock+300000).toISOString(),conversation_id:conversation}));return;
    }
    assert.equal(req.headers.authorization,'Bearer '+token);
    await new Promise(resolve=>{release=resolve;entered();});
    res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({messages:[{content:'must not escape after logout'}]}));
  });
  await new Promise(resolve=>upstream.listen(0,'127.0.0.1',resolve));
  t.after(()=>{release?.();upstream.closeAllConnections();return new Promise(resolve=>upstream.close(resolve));});
  const host=await startHost({gatewayOrigin:`http://127.0.0.1:${upstream.address().port}`,hostSecret:secret,now:()=>clock});t.after(()=>host.close());const call=client(host.origin);
  const login=await call('/demo/login',{method:'POST',body:{account:'lin'}});
  const session=await call('/host/chat-session',{method:'POST',body:{},cookie:login.cookie});assert.equal(session.status,200);
  const pending=call('/chat/v1/messages',{cookie:login.cookie,token});await waiting;
  assert.equal((await call('/demo/logout',{method:'POST',body:{},cookie:login.cookie})).status,204);release();
  const result=await pending;assert.equal(result.status,401);assert.doesNotMatch(JSON.stringify(result.data),/must not escape/);
  const next=await call('/demo/login',{method:'POST',body:{account:'lin'}});clock+=1800001;
  assert.equal((await call('/host/chat-session',{method:'POST',body:{},cookie:next.cookie})).status,401);
});
