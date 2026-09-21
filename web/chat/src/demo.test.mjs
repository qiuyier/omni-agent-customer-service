import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeStorage, normalizeReplyMode, replyRequestTimeout, createChatAPI } from './protocol.mjs';
import { demoPresentation } from './demo.mjs';

test('persistence labels depend only on an explicit server storage enum', () => {
  assert.match(demoPresentation('postgres').description, /重新选择同一演示账号，历史仍保留/);
  assert.match(demoPresentation('memory').description, /重启后清空/);
  for (const value of [undefined, null, true, 'production', { storage: 'postgres' }]) {
    assert.equal(normalizeStorage(value), null);
    assert.match(demoPresentation(value).description, /尚未确认/);
    assert.doesNotMatch(demoPresentation(value).description, /历史仍保留/);
  }
});

test('session renewal keeps PostgreSQL metadata without exposing its bearer', async () => {
  const session = (token) => ({access_token: token, conversation_id:'same-conversation', storage:'postgres', reply_mode:'ai', ai_api_key:'must-not-expose', user:{id:'lin'}, demo:true});
  const json = (value,status=200) => new Response(JSON.stringify(value), {status});
  const responses = [json(session('old')), json({error:'unauthorized'},401), json(session('new')), json({outcome:'replied',message_id:'message'})];
  const calls = [];
  const api = createChatAPI(async (path,options) => {calls.push({path,body:options.body});return responses.shift();});
  const opened = await api.open();
  assert.equal(opened.storage,'postgres');
  assert.equal(opened.access_token,undefined);
  assert.equal(opened.reply_mode,'ai');
  assert.equal(opened.ai_api_key,undefined);
  await api.send({event_id:'original-event',content:'original-content'});
  assert.equal(calls[1].body,calls[3].body);
});

test('reply labels and waiting budgets require an explicit safe server mode', () => {
  assert.match(demoPresentation('postgres','ai').label, /AI 回复/);
  assert.match(demoPresentation('memory','fake').label, /模拟回复/);
  assert.match(demoPresentation('postgres','ai').capabilities, /回答需核实/);
  assert.match(demoPresentation('postgres','ai').description, /模拟登录不是真实认证/);
  assert.equal(replyRequestTimeout('ai'),150000);
  assert.equal(replyRequestTimeout('fake'),45000);
  for (const value of [undefined,null,true,'AI','production',{reply_mode:'ai'}]) {
    assert.equal(normalizeReplyMode(value),null);
    assert.equal(replyRequestTimeout(value),45000);
    assert.match(demoPresentation('postgres',value).label, /回复模式未确认/);
    assert.doesNotMatch(demoPresentation('postgres',value).label, /AI 回复|模拟回复/);
  }
});

test('401 renewal uses the new mode waiting budget and the same event', async (t) => {
  const timers=[];
  const originalSetTimeout=globalThis.setTimeout;
  t.mock.method(globalThis,'setTimeout',(fn,ms,...args)=>{timers.push(ms);return originalSetTimeout(fn,ms,...args);});
  const json=(value,status=200)=>new Response(JSON.stringify(value),{status});
  const session=(mode)=>({access_token:'test-token',conversation_id:'same',user:{id:'lin'},reply_mode:mode});
  const responses=[json(session('fake')),json({error:'unauthorized'},401),json(session('ai')),json({outcome:'replied'})];
  const bodies=[];
  const api=createChatAPI(async (_path,options)=>{bodies.push(options.body);return responses.shift();});
  await api.open();
  await api.send({event_id:'unchanged',content:'unchanged'});
  assert.deepEqual(timers,[45000,45000,45000,150000]);
  assert.equal(bodies[1],bodies[3]);
});
