// Existing standalone-host demo authentication, NOT production password login.
export function validateStandalone(c, run = false) {
  if (!c || c.adapter !== 'standalone-demo' || c.environment !== 'isolated-standalone-lab') throw Error('invalid_config');
  const u = new URL(c.origin);
  if (u.protocol !== 'http:' || u.hostname !== '127.0.0.1' || !u.port || u.username || u.password || u.pathname !== '/' || u.search || u.hash || c.origin !== u.origin) throw Error('loopback_required');
  if (!Number.isInteger(c.timeout_ms) || c.timeout_ms < 100 || c.timeout_ms > 30000) throw Error('invalid_timeout');
  if (run) {
    for (const who of ['a', 'b', 'staff']) {
      if (!/^[A-Za-z0-9_-]{1,128}$/.test(c.accounts?.[who]?.account || '')) throw Error('account_required');
      const name = who === 'staff' ? 'standalone_desk' : 'standalone_login';
      if (typeof c.old_cookies?.[who] !== 'string' || !new RegExp('^' + name + '=[A-Za-z0-9_-]{43}$').test(c.old_cookies[who])) throw Error('cookie_required');
    }
    if (c.accounts.a.account === c.accounts.b.account || new Set(Object.values(c.old_cookies)).size !== 3) throw Error('distinct_identities_required');
  }
  return c;
}

export function createStandalone(c, {fetchImpl = fetch} = {}) {
  validateStandalone(c, true);
  const cookies = {}, sessions = {}, created = new Set();
  async function request(path, {method = 'POST', body = {}, cookie, capability, expect = [200]} = {}) {
    const headers = {Origin: c.origin, 'Content-Type': 'application/json'};
    if (cookie) headers.Cookie = cookie;
    if (capability) headers.Authorization = 'Bearer ' + capability;
    const controller = new AbortController(), timer = setTimeout(() => controller.abort(), c.timeout_ms);
    try {
      const r = await fetchImpl(c.origin + path, {method, headers, redirect: 'error', signal: controller.signal,
        body: method === 'GET' ? undefined : JSON.stringify(body)});
      if (!expect.includes(r.status)) { await r.body?.cancel(); throw Error('unexpected_status'); }
      if (r.status === 401 || r.status === 204) { await r.body?.cancel(); return {status: r.status}; }
      const chunks = []; let size = 0;
      const reader = r.body.getReader();
      for (;;) {
        const {done, value} = await reader.read(); if (done) break;
        size += value.length; if (size > 1048576) { await reader.cancel(); throw Error('response_limit'); }
        chunks.push(Buffer.from(value));
      }
      return {status: r.status, data: JSON.parse(Buffer.concat(chunks).toString('utf8')),
        cookie: r.headers.get('set-cookie')?.split(';')[0]};
    } finally { clearTimeout(timer); }
  }
  const protectedPath = who => who === 'staff' ? '/desk/queue' : '/host/chat-session';
  const protectedCall = (who, cookie, expect) => request(protectedPath(who), {cookie, method: who === 'staff' ? 'GET' : 'POST', expect});
  const chat = async (who, path, body, extra = {}) => (await request(path, {
    cookie: cookies[who], capability: sessions[who]?.access_token,
    method: body === undefined ? 'GET' : 'POST', body, ...extra})).data;
  return {
    async preflight() { validateStandalone(c, true); },
    async oldDenied(who) { return (await protectedCall(who, c.old_cookies[who], [401])).status === 401; },
    async login(who) {
      const r = await request(who === 'staff' ? '/desk/login' : '/demo/login', {body: {account: c.accounts[who].account}});
      const name = who === 'staff' ? 'standalone_desk' : 'standalone_login';
      if (!new RegExp('^' + name + '=[A-Za-z0-9_-]{43}$').test(r.cookie || '')) throw Error('login_failed');
      cookies[who] = r.cookie; created.add(who);
    },
    async staff() { return (await chat('staff', '/desk/me')).agent; },
    async session(who) {
      const s = await chat(who, '/host/chat-session', {});
      if (s.demo !== true || s.reply_mode !== 'fake' || s.storage !== 'postgres' || s.human_enabled !== true || typeof s.access_token !== 'string') throw Error('unsafe_mode');
      sessions[who] = s; return s;
    },
    async crossDenied() { return (await request('/chat/v1/messages', {method: 'GET', cookie: cookies.a, capability: sessions.b.access_token, expect: [401]})).status === 401; },
    history: who => chat(who, '/chat/v1/messages'),
    newConsultation: () => chat('a', '/chat/v1/consultations', {}),
    send: body => chat('a', '/chat/v1/messages', body, {expect: [200, 201]}),
    receipt: body => chat('a', '/chat/v1/message-receipt', body),
    handoff: () => chat('a', '/chat/v1/handoff', {}),
    desk: (action, conversation_id, fields = {}) => chat('staff', '/desk/action', {action, conversation_id, ...fields}),
    async cleanup() {
      let failed = false;
      for (const who of created) {
        try {
          await request(who === 'staff' ? '/desk/logout' : '/demo/logout', {cookie: cookies[who], expect: [204]});
          await protectedCall(who, cookies[who], [401]);
        } catch { failed = true; }
      }
      for (const who of Object.keys(cookies)) { delete cookies[who]; delete sessions[who]; }
      created.clear(); if (failed) throw Error('cleanup_failed');
    },
  };
}
