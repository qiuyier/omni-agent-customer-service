export const MAX_BYTES = 4000;
export const byteLength = (value) => new TextEncoder().encode(value).length;
export const validContent = (value) => typeof value === 'string' && value.trim().length > 0 && byteLength(value) <= MAX_BYTES;
export const pendingKey = (conversationID) => `webchat:pending:v1:${conversationID}`;
export const normalizeStorage = (value) => value === 'memory' || value === 'postgres' ? value : null;
export const normalizeReplyMode = (value) => value === 'fake' || value === 'ai' ? value : null;
export const replyRequestTimeout = (replyMode) => normalizeReplyMode(replyMode) === 'ai' ? 150000 : 45000;

export function readPending(storage, conversationID) {
  const raw = storage.getItem(pendingKey(conversationID));
  if (!raw) return null;
  let value;
  try { value = JSON.parse(raw); } catch { throw new Error('invalid_pending'); }
  if (value?.version !== 1 || value.conversation_id !== conversationID ||
      typeof value.event_id !== 'string' || !/^[0-9a-f-]{36}$/i.test(value.event_id) || !validContent(value.content)) {
    throw new Error('invalid_pending');
  }
  return value;
}

export function savePending(storage, conversationID, pending) {
  storage.setItem(pendingKey(conversationID), JSON.stringify({
    version: 1, conversation_id: conversationID,
    event_id: pending.event_id, content: pending.content,
  }));
}

export function probeStorage(storage, conversationID) {
  const key = `${pendingKey(conversationID)}:probe`;
  storage.setItem(key, '1');
  storage.removeItem(key);
}

export class APIError extends Error {
  constructor(code, status = 0) {
    super(code);
    this.name = 'APIError';
    this.code = code;
    this.status = status;
  }
}

const knownErrors = new Set(['unauthorized', 'invalid_request', 'busy', 'rate_limited', 'event_conflict', 'unavailable', 'conversation_closed', 'not_found']);

export async function requestJSON(path, { method = 'GET', body, token, signal, fetcher = globalThis.fetch, timeout = 45000, chatTokenRecovery = false } = {}) {
  const controller = new AbortController();
  const abort = () => controller.abort();
  if (signal?.aborted) controller.abort();
  else signal?.addEventListener('abort', abort, { once: true });
  const timer = setTimeout(abort, timeout);
  try {
    const headers = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (token) headers.Authorization = `Bearer ${token}`;
    const response = await fetcher(path, {
      method, headers, credentials: 'same-origin', cache: 'no-store',
      ...(chatTokenRecovery?{chatTokenRecovery:true}:{}),
      body: body === undefined ? undefined : JSON.stringify(body), signal: controller.signal,
    });
    if (response.status === 204 && response.ok) return null;
    let result;
    try { result = await response.json(); } catch { throw new APIError('unavailable', response.status); }
    if (!response.ok) {
      const fallback = response.status === 401 ? 'unauthorized' : response.status === 429 ? 'rate_limited' : 'unavailable';
      throw new APIError(knownErrors.has(result?.error) ? result.error : fallback, response.status);
    }
    return result;
  } catch (error) {
    if (signal?.aborted) throw new DOMException('Aborted', 'AbortError');
    if (error instanceof APIError) throw error;
    throw new APIError('unavailable');
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener('abort', abort);
  }
}

// The bearer token exists only in this closure. It never enters React state or browser storage.
export function createChatAPI(fetcher = globalThis.fetch) {
  let token = '';
  let current = null;
  let revision = 0;
  let renewal = null;
  const acquire = async (signal, expectedConversation) => {
    const started = revision;
    const result = await requestJSON('/host/chat-session', { method: 'POST', body: {}, signal, fetcher });
    // An old request may resolve after the UI has switched accounts or unmounted.
    // It must not restore its capability into the new page session.
    if (started !== revision || signal?.aborted) throw new DOMException('Aborted', 'AbortError');
    if (!result?.access_token || !result?.conversation_id || !result?.user?.id) throw new APIError('unavailable');
    if (expectedConversation && result.conversation_id !== expectedConversation) {
      token = '';
      throw new APIError('account_changed');
    }
    token = result.access_token;
    current = { conversation_id: result.conversation_id, expires_at: result.expires_at, user: result.user, demo: result.demo, storage: normalizeStorage(result.storage), reply_mode: normalizeReplyMode(result.reply_mode), human_enabled: result.human_enabled===true, new_consultation_enabled:result.new_consultation_enabled===true };
    return current;
  };
  // Renewal belongs to this API lifetime, not to its first caller. Each caller
  // can cancel independently; the last departing caller cancels the transport.
  const renew = (signal, expectedConversation) => {
    if (signal?.aborted) return Promise.reject(new DOMException('Aborted', 'AbortError'));
    if (!renewal) {
      const flight = { controller: new AbortController(), waiters: 0, settled: false };
      renewal = flight;
      flight.promise = acquire(flight.controller.signal, expectedConversation).finally(() => {
        flight.settled = true;
        if (renewal === flight) renewal = null;
      });
    }
    const flight = renewal;
    flight.waiters++;
    return new Promise((resolve, reject) => {
      let done = false;
      const finish = (callback, value) => {
        if (done) return;
        done = true;
        signal?.removeEventListener('abort', abort);
        flight.controller.signal.removeEventListener('abort', abort);
        flight.waiters--;
        if (!flight.waiters && !flight.settled) {
          if (renewal === flight) renewal = null;
          flight.controller.abort();
        }
        callback(value);
      };
      const abort = () => finish(reject, new DOMException('Aborted', 'AbortError'));
      signal?.addEventListener('abort', abort, { once: true });
      flight.controller.signal.addEventListener('abort', abort, { once: true });
      flight.promise.then(value => finish(resolve, value), error => finish(reject, error));
    });
  };
  const authorized = async (method, body, signal, path='/chat/v1/messages') => {
    if (!current || !token) throw new APIError('unauthorized', 401);
    const started=revision;
    const check=()=>{if(started!==revision||signal?.aborted)throw new DOMException('Aborted','AbortError');};
    const expected = current.conversation_id;
    const attemptedToken = token;
    try {
      const result=await requestJSON(path, { method, body, token, signal, fetcher, chatTokenRecovery:true, timeout: method === 'POST' ? replyRequestTimeout(current.reply_mode) : 45000 });
      check();return result;
    } catch (error) {
      check();
      if (error.status !== 401) throw error;
      // A delayed 401 for an old token must not replace a newer capability.
      if (token === attemptedToken) await renew(signal, expected);
      if (!current || current.conversation_id !== expected || !token) throw new APIError('account_changed');
      check();
      // The same body object (and event_id) is reused after token renewal.
      const result=await requestJSON(path, { method, body, token, signal, fetcher, timeout: method === 'POST' ? replyRequestTimeout(current.reply_mode) : 45000 });
      check();return result;
    }
  };
  return {
    open: (signal) => acquire(signal),
    handoff: (signal) => authorized('POST',{},signal,'/chat/v1/handoff'),
    nextRound: (signal) => authorized('POST',{},signal,'/chat/v1/consultations'),
    previousRound: (signal) => authorized('GET',undefined,signal,'/chat/v1/previous-consultation'),
    history: (signal) => authorized('GET', undefined, signal),
    receipt: (pending,signal) => authorized('POST',{event_id:pending.event_id,content:pending.content},signal,'/chat/v1/message-receipt'),
    send: (pending, signal) => authorized('POST', { event_id: pending.event_id, content: pending.content }, signal),
    clear: () => { revision++; token = ''; current = null; const flight = renewal; renewal = null; flight?.controller.abort(); },
  };
}

export const errorMessage = (error) => ({
  unauthorized: '登录已失效。请返回示例站点重新选择演示账号。',
  account_changed: '账号或咨询轮次已发生变化。请重新载入当前页面，确认后继续；旧消息不会自动重发。',
  invalid_request: '消息未被接受，请检查内容后重试。',
  busy: '当前会话正在处理另一条消息，请稍后重试。',
  rate_limited: '发送得有些频繁，请稍等片刻再重试。',
  event_conflict: '这条待确认消息的标识发生冲突。请保留内容并联系服务维护者。',
  unavailable: '暂时无法确认服务响应，请检查连接后重试。',
  conversation_closed: '当前会话已结束，暂时无法继续发送。',
  not_found: '暂时无法找到当前会话。请重新载入页面。',
})[error?.code] || '暂时无法连接服务，请稍后重试。';
