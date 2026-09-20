import test from 'node:test';
import assert from 'node:assert/strict';
import { byteLength, validContent, savePending, readPending, pendingKey, probeStorage, createChatAPI, requestJSON } from './protocol.mjs';

const reply = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } });
const session = (id = 'conversation-lin', token = 'short-lived-test-token') => ({ access_token: token, conversation_id: id, user: { id: 'lin', display_name: '林知夏' }, demo: true });

test('content limit counts UTF-8 bytes, not JavaScript characters', () => {
  assert.equal(byteLength('你好'), 6);
  assert.equal(validContent('   '), false);
  assert.equal(validContent('a'.repeat(4000)), true);
  assert.equal(validContent('中'.repeat(1334)), false);
});

test('pending records preserve event and content, isolate conversations and never contain a token', () => {
  const map = new Map();
  const storage = { setItem: (k, v) => map.set(k, v), getItem: (k) => map.get(k), removeItem: (k) => map.delete(k) };
  const pending = { event_id: '2c8b50a1-0500-4000-9000-361942851810', content: '保持原样\n 请重试', access_token: 'must-not-persist' };
  probeStorage(storage, 'first');
  savePending(storage, 'first', pending);
  assert.equal(readPending(storage, 'first').event_id, pending.event_id);
  assert.equal(readPending(storage, 'first').content, pending.content);
  assert.equal(readPending(storage, 'second'), null);
  assert.equal(map.get(pendingKey('first')).includes('must-not-persist'), false);
  storage.setItem(pendingKey('first'), '{bad json');
  assert.throws(() => readPending(storage, 'first'), /invalid_pending/);
});

test('blocked session storage is explicitly detectable', () => {
  assert.throws(() => probeStorage({ setItem: () => { throw new Error('blocked'); } }, 'first'), /blocked/);
});

test('401 renews token once and retries the exact message identity and content', async () => {
  const calls = [];
  const responses = [reply(session()), reply({ error: 'unauthorized' }, 401), reply(session('conversation-lin', 'renewed-test-token')), reply({ outcome: 'replied', message_id: 'message-1' })];
  const api = createChatAPI(async (path, options) => { calls.push({ path, ...options }); return responses.shift(); });
  const opened = await api.open();
  assert.equal(opened.access_token, undefined);
  const pending = { event_id: 'same-event', content: '原始内容' };
  assert.equal((await api.send(pending)).outcome, 'replied');
  assert.deepEqual(calls.map((c) => c.path), ['/host/chat-session', '/chat/v1/messages', '/host/chat-session', '/chat/v1/messages']);
  assert.equal(calls[1].body, calls[3].body);
  assert.equal(calls[3].headers.Authorization, 'Bearer renewed-test-token');
  assert.equal(calls[0].headers.Authorization, undefined);
});

test('refresh performs session acquisition and history only, never POSTs a message', async () => {
  const calls = [];
  const responses = [reply(session()), reply({ messages: [], conversation_id: 'conversation-lin' })];
  const api = createChatAPI(async (path, options) => { calls.push({ path, ...options }); return responses.shift(); });
  await api.open();
  await api.history();
  assert.equal(calls[1].method, 'GET');
  assert.equal(calls[1].body, undefined);
});

test('renewal never resends a pending message into a different account conversation', async () => {
  const calls = [];
  const responses = [reply(session()), reply({ error: 'unauthorized' }, 401), reply(session('conversation-zhou'))];
  const api = createChatAPI(async (path, options) => { calls.push({ path, ...options }); return responses.shift(); });
  await api.open();
  await assert.rejects(api.send({ event_id: 'same-event', content: '私有内容' }), { code: 'account_changed' });
  assert.equal(calls.length, 3);
});

test('an uncertain network failure is not automatically replayed', async () => {
  let calls = 0;
  const api = createChatAPI(async () => { if (calls++ === 0) return reply(session()); throw new TypeError('offline'); });
  await api.open();
  await assert.rejects(api.send({ event_id: 'same-event', content: '内容' }), { code: 'unavailable' });
  assert.equal(calls, 2);
});

test('unknown host codes normalize by status without exposing raw error text', async () => {
  await assert.rejects(requestJSON('/host/chat-session', { fetcher: async () => reply({ error: 'host_login_required' }, 401) }), { code: 'unauthorized' });
  await assert.rejects(requestJSON('/host/chat-session', { fetcher: async () => reply({ error: 'demo_capacity_reached' }, 429) }), { code: 'rate_limited' });
});

test('an old identity exchange cannot restore a capability after session clear', async () => {
  let complete;
  const api = createChatAPI(() => new Promise((resolve) => { complete = resolve; }));
  const opening = api.open();
  api.clear();
  complete(reply(session()));
  await assert.rejects(opening, { name: 'AbortError' });
  await assert.rejects(api.history(), { code: 'unauthorized' });
});
