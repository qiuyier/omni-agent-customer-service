import { APIError, requestJSON, replyRequestTimeout } from './protocol.mjs';

export const LOGIN_KEY = 'LOGIN_STATUS_TOKEN';
export const replyDescription = mode => mode === 'ai'
  ? '当前启用 AI 回复，对话内容将用于模型生成。尚未接入业务查询或操作工具，请勿发送密码、验证码或真实个人信息。历史记录可能包含此前的固定测试回复。'
  : mode === 'fake' ? '当前使用固定测试回复，不调用模型，不查询订单。' : '回复模式未确认，请重新连接后核对。';
// This CRMEB H5 build stores string values as plain localStorage strings.
export function readLogin(storage) {
  try {
    const value = storage.getItem(LOGIN_KEY);
    return typeof value === 'string' && /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(value) && value.length <= 4096 ? value : '';
  } catch { return ''; }
}

export function eventID(cryptoAPI = globalThis.crypto) {
  const b = cryptoAPI.getRandomValues(new Uint8Array(16));
  b[6] = (b[6] & 15) | 64; b[8] = (b[8] & 63) | 128;
  const h = [...b].map(v => v.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0,8)}-${h.slice(8,12)}-${h.slice(12,16)}-${h.slice(16,20)}-${h.slice(20)}`;
}

export function createCRMEBAPI({ getLogin, fetcher = globalThis.fetch }) {
  let login = '', capability = '', current = null, revision = 0;
  const check = (started) => {
    if (started !== revision) throw new DOMException('Aborted', 'AbortError');
    if (!login || getLogin() !== login) throw new APIError('account_changed', 401);
  };
  const call = async (path, options = {}) => {
    const started = revision; check(started);
    const pinned = login;
    const wrapped = async (url, init) => {
      const headers = { ...init.headers, 'Authori-zation': `Bearer ${pinned}` };
      if (capability && !path.endsWith('/session')) headers['X-Omni-Chat-Token'] = capability;
      return fetcher(url, { ...init, headers, redirect: 'error' });
    };
    let result;
    try { result = await requestJSON(path, { ...options, fetcher: wrapped }); }
    catch (error) { check(started); throw error; }
    check(started); return result;
  };
  const acquire = async (signal, expected) => {
    const data = await call('/api/omni-chat/session', { method: 'POST', body: {}, signal });
    if (!/^chat_[A-Za-z0-9_-]{43}$/.test(data?.access_token || '') || !/^wbc_[a-f0-9]{64}$/.test(data?.conversation_id || '') || !Number.isFinite(Date.parse(data.expires_at))) throw new APIError('unavailable');
    if (expected && expected !== data.conversation_id) throw new APIError('account_changed', 401);
    capability = data.access_token;
    current = { conversation_id: data.conversation_id, expires_at: data.expires_at,
      storage: ['postgres', 'memory'].includes(data.storage) ? data.storage : 'unknown',
      reply_mode: ['fake', 'ai'].includes(data.reply_mode) ? data.reply_mode : 'unknown', human_enabled:data.human_enabled===true, new_consultation_enabled:data.new_consultation_enabled===true };
    return current;
  };
  const authorized = async (method, body, signal, path='/api/omni-chat/messages') => {
    if (!current) throw new APIError('unauthorized', 401);
    const expected = current.conversation_id;
    const timeout = () => method === 'POST' ? replyRequestTimeout(current.reply_mode) : 45000;
    try { return await call(path, { method, body, signal, timeout: timeout() }); }
    catch (error) {
      if (error.status !== 401 || error.code === 'account_changed') throw error;
      await acquire(signal, expected);
      return call(path, { method, body, signal, timeout: timeout() });
    }
  };
  return {
    open: signal => { revision++; login = getLogin(); capability = ''; current = null; if (!login) return Promise.reject(new APIError('unauthorized', 401)); return acquire(signal); },
    handoff: signal => authorized('POST',{},signal,'/api/omni-chat/handoff'),
    nextRound: signal => authorized('POST',{},signal,'/api/omni-chat/consultations'),
    history: signal => authorized('GET', undefined, signal),
    send: (pending, signal) => authorized('POST', { event_id: pending.event_id, content: pending.content }, signal),
    changed: () => !!login && getLogin() !== login,
    clear: () => { revision++; login = ''; capability = ''; current = null; },
  };
}
