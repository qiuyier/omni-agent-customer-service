import test from 'node:test';
import assert from 'node:assert/strict';
import { startDemo } from './demo.mjs';

test('non-CRMEB repair host: identity, knowledge, history, replay and isolation', {timeout:180000}, async t => {
  const demo = await startDemo();
  t.after(() => demo.stop());
  for (const path of ['/', '/chat/', '/assets/app.js', '/assets/app.css']) {
    const response = await fetch(demo.origin+path, {signal:AbortSignal.timeout(10000)});
    assert.equal(response.status,200);
    assert.ok((await response.text()).length > 0);
  }
  const request = async (path, {method='GET',body,cookie,token,origin=demo.origin}={}) => {
    const headers = {};
    if (method === 'POST') Object.assign(headers, {'Content-Type':'application/json',Origin:origin});
    if (cookie) headers.Cookie=cookie;
    if (token) headers.Authorization=`Bearer ${token}`;
    const response = await fetch(demo.origin+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(10000)});
    const text = await response.text();
    return {status:response.status, cookie:response.headers.get('set-cookie')?.split(';')[0], data:text?JSON.parse(text):null};
  };
  const login = async account => {
    const result = await request('/demo/login',{method:'POST',body:{account}});
    assert.equal(result.status,200);assert.ok(result.cookie);return result.cookie;
  };
  const session = async cookie => {
    const result = await request('/host/chat-session',{method:'POST',body:{},cookie});
    assert.equal(result.status,200);assert.ok(result.data.access_token);return result.data;
  };
  const history = async (cookie,token) => {
    const result=await request('/chat/v1/messages',{cookie,token});
    assert.equal(result.status,200);return result.data;
  };
  assert.equal((await request('/host/chat-session',{method:'POST',body:{}})).status,401);
  const a=await login('lin'), b=await login('zhou');
  const sa=await session(a), sb=await session(b);
  assert.notEqual(sa.conversation_id,sb.conversation_id);
  assert.equal((await history(a,sa.access_token)).messages.length,0);
  assert.equal((await request('/host/chat-session',{method:'POST',body:{customer_id:'zhou'},cookie:a})).status,400);
  assert.equal((await request('/chat/v1/messages',{cookie:b,token:sa.access_token})).status,401);
  assert.equal((await request('/host/chat-session',{method:'POST',body:{},cookie:a,origin:'http://foreign.invalid'})).status,403);
  const event={event_id:'same-source-event-1',content:'如何报修？用户甲专用标记'};
  const first=await request('/chat/v1/messages',{method:'POST',body:event,cookie:a,token:sa.access_token});
  assert.equal(first.status,200,JSON.stringify(demo.failures));assert.equal(first.data.outcome,'replied');
  const replay=await request('/chat/v1/messages',{method:'POST',body:event,cookie:a,token:sa.access_token});
  assert.deepEqual(replay.data,first.data);assert.equal(demo.calls.length,1);
  const refreshed=await session(a);
  assert.equal(refreshed.conversation_id,sa.conversation_id);
  const ha=await history(a,refreshed.access_token);
  assert.equal(ha.messages.length,2);assert.match(ha.messages[1].content,/设备服务 → 新建报修/);
  assert.equal((await history(b,sb.access_token)).messages.length,0);
  const second=await request('/chat/v1/messages',{method:'POST',body:{event_id:event.event_id,content:'受理时间是几点？用户乙专用标记'},cookie:b,token:sb.access_token});
  assert.equal(second.status,200);assert.notEqual(second.data.message_id,first.data.message_id);
  const hb=await history(b,sb.access_token);
  assert.equal(hb.messages.length,2);assert.match(hb.messages[1].content,/10:00—16:00/);
  assert.doesNotMatch(JSON.stringify(hb),/用户甲专用标记/);
  assert.doesNotMatch(JSON.stringify(demo.calls[1]),/用户甲专用标记/);
  const unknown=await request('/chat/v1/messages',{method:'POST',body:{event_id:'unknown-2',content:'火星天气怎么样？'},cookie:a,token:sa.access_token});
  assert.equal(unknown.status,200);
  const unknownHistory=await history(a,sa.access_token);
  assert.match(unknownHistory.messages.at(-1).content,/当前资料无法确认/);
  assert.doesNotMatch(JSON.stringify(demo.calls[2]),/用户乙专用标记/);
  assert.match(JSON.stringify(demo.calls[2]),/用户甲专用标记/);
  assert.equal((await request('/demo/logout',{method:'POST',body:{},cookie:a})).status,204);
  assert.equal((await request('/chat/v1/messages',{cookie:a,token:sa.access_token})).status,401);
  const newCookie=await login('lin');
  const again=await session(newCookie);
  assert.equal(again.conversation_id,sa.conversation_id);
  assert.equal((await history(newCookie,again.access_token)).messages.length,4);
  assert.equal(demo.calls.length,3);
  assert.deepEqual(demo.failures,[]);
});
