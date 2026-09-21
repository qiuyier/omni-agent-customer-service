import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeStorage, createChatAPI } from './protocol.mjs';
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
  const session = (token) => ({access_token: token, conversation_id:'same-conversation', storage:'postgres', user:{id:'lin'}, demo:true});
  const json = (value,status=200) => new Response(JSON.stringify(value), {status});
  const responses = [json(session('old')), json({error:'unauthorized'},401), json(session('new')), json({outcome:'replied',message_id:'message'})];
  const calls = [];
  const api = createChatAPI(async (path,options) => {calls.push({path,body:options.body});return responses.shift();});
  const opened = await api.open();
  assert.equal(opened.storage,'postgres');
  assert.equal(opened.access_token,undefined);
  await api.send({event_id:'original-event',content:'original-content'});
  assert.equal(calls[1].body,calls[3].body);
});
