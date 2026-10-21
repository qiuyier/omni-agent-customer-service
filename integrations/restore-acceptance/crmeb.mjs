// CRMEB-specific identity and HTTP mapping; no Docker/database defaults.
export function validateConfig(c, run = false) {
  if (!c || c.adapter !== 'crmeb' || c.environment !== 'isolated-restored-copy') throw Error('invalid_config');
  const u = new URL(c.origin);
  if (u.protocol !== 'http:' || u.hostname !== '127.0.0.1' || !u.port || u.username || u.password || u.pathname !== '/' || u.search || u.hash || c.origin !== u.origin) throw Error('loopback_required');
  if (!Number.isInteger(c.timeout_ms) || c.timeout_ms < 100 || c.timeout_ms > 30000) throw Error('invalid_timeout');
  if (run) {
    for (const who of ['a', 'b', 'staff']) {
      if (typeof c.accounts?.[who]?.account !== 'string' || !c.accounts[who].account || typeof c.accounts[who].password !== 'string' || !c.accounts[who].password ||
          typeof c.old_tokens?.[who] !== 'string' || !/^[A-Za-z0-9_.-]{20,4096}$/.test(c.old_tokens[who])) throw Error('credentials_required');
    }
    if (c.accounts.a.account === c.accounts.b.account || new Set(Object.values(c.old_tokens)).size !== 3) throw Error('distinct_identities_required');
  }
  return c;
}

export function createCRMEB(c, {fetchImpl = fetch} = {}) {
  validateConfig(c, true);
  const tokens = {}, sessions = {}, created = new Set();
  async function request(path, {method = 'POST', body = {}, token, capability, expect = [200]} = {}) {
    const headers = {Origin: c.origin, 'Content-Type': 'application/json'};
    if (token) headers['Authori-zation'] = 'Bearer ' + token;
    if (capability) headers['X-Omni-Chat-Token'] = capability;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), c.timeout_ms);
    try {
      const r = await fetchImpl(c.origin + path, {method, headers, redirect: 'error', signal: controller.signal,
        body: method === 'GET' ? undefined : JSON.stringify(body)});
      if (!expect.includes(r.status)) { await r.body?.cancel(); throw Error('unexpected_status'); }
      if (r.status === 401) { await r.body?.cancel(); return {denied: true}; }
      const reader = r.body.getReader(); const chunks = []; let length = 0;
      for (;;) { const {done, value} = await reader.read(); if (done) break;
        length += value.length; if (length > 1_048_576) { await reader.cancel(); throw Error('response_limit'); } chunks.push(Buffer.from(value)); }
      return JSON.parse(Buffer.concat(chunks).toString('utf8'));
    } finally { clearTimeout(timer); }
  }
  const chat = (who, path, body, extra = {}) => request('/api/omni-chat/' + path, {
    method: body === undefined ? 'GET' : 'POST', body, token: tokens[who], capability: sessions[who]?.access_token, ...extra});
  return {
    async preflight() { validateConfig(c, true); },
    async oldDenied(who) {
      const r = await request('/api/omni-chat/' + (who === 'staff' ? 'desk/me' : 'session'), {
        method: who === 'staff' ? 'GET' : 'POST', token: c.old_tokens[who], expect: [401]});
      return r.denied === true;
    },
    async login(who) {
      const d = await request(who === 'staff' ? '/kefuapi/login' : '/api/login', {body: c.accounts[who]});
      if (typeof d?.data?.token !== 'string' || !d.data.token) throw Error('login_failed');
      tokens[who] = d.data.token; created.add(who);
    },
    async staff() { return (await chat('staff', 'desk/me')).agent; },
    async session(who) {
      const s = await chat(who, 'session', {});
      if (s.reply_mode !== 'fake' || s.storage !== 'postgres' || typeof s.access_token !== 'string' || !s.access_token) throw Error('unsafe_mode');
      sessions[who] = s; return s;
    },
    async crossDenied() { return (await chat('a', 'messages', undefined, {capability: sessions.b.access_token, expect: [401]})).denied === true; },
    history: who => chat(who, 'messages'),
    newConsultation: () => chat('a', 'consultations', {}),
    send: body => chat('a', 'messages', body, {expect: [200, 201]}),
    receipt: body => chat('a', 'message-receipt', body),
    handoff: () => chat('a', 'handoff', {}),
    desk: (action, conversation_id, fields = {}) => chat('staff', 'desk/action', {action, conversation_id, ...fields}),
    async cleanup() {
      let failed = false;
      for (const who of created) {
        try {
          const d = await request(who === 'staff' ? '/kefuapi/user/logout' : '/api/logout', {
            method: who === 'staff' ? 'POST' : 'GET', token: tokens[who]});
          if (d.status !== 200) failed = true;
        } catch { failed = true; }
      }
      for (const who of Object.keys(tokens)) delete tokens[who];
      created.clear();
      if (failed) throw Error('cleanup_failed');
    },
  };
}
