import React, { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { reconcilePending } from './pending-receipt.mjs';
import { createCRMEBAPI, readLogin, eventID, replyDescription } from './crmeb.mjs';
import { byteLength, validContent, probeStorage, readPending, savePending, pendingKey } from './protocol.mjs';

function Chat() {
  const api = useRef(null);
  if (!api.current) api.current = createCRMEBAPI({ getLogin: () => readLogin(localStorage) });
  const lifetime = useRef(null), epoch = useRef(0), busy = useRef(false);
  const [session, setSession] = useState(null), [messages, setMessages] = useState([]);
  const [status,setStatus]=useState('bot_active');
  const [phase, setPhase] = useState('loading'), [error, setError] = useState('');
  const [draft, setDraft] = useState(''), [pending, setPending] = useState(null), [sending, setSending] = useState(false);
  const [canStore, setCanStore] = useState(false);
  function resetIdentity() {
    epoch.current++; lifetime.current?.abort(); api.current.clear(); busy.current = false;
    setSession(null); setMessages([]); setPending(null); setDraft(''); setSending(false);
    setCanStore(false); setPhase('unauthorized'); setError('登录已退出或账号已切换，请重新登录后连接。');
  }
  function failure(e) {
    if (e.name === 'AbortError') return;
    if (e.status === 401 || e.code === 'account_changed') { resetIdentity(); return; }
    setError((e.status === 404 ? '测试服务尚未启用，请联系维护者。' : e.status === 429 ? '请求较多，请稍后重试。' : '暂时无法确认服务响应。请检查连接后重试，不要重复创建消息。') + `（状态：${e.status || 0}）`);
  }
  async function confirmPending(started, s, recovered) {
    try {
      const cleared = await reconcilePending({api:api.current,storage:sessionStorage,conversationID:s.conversation_id,pending:recovered,signal:lifetime.current.signal,active:()=>started===epoch.current&&!api.current.changed()});
      if (cleared) { setPending(null); setError('已确认原消息送达，已清除待确认提示；这不代表已获得回复。'); }
    } catch (e) {
      if (started !== epoch.current || e.name === 'AbortError') return;
      if (e.status === 401 || e.code === 'account_changed') { failure(e); return; }
      setError('暂未能核实原消息的接收结果，待确认内容已保留，不会自动重发。');
    }
  }
  async function connect() {
    lifetime.current?.abort(); const controller = new AbortController(); lifetime.current = controller;
    const started = ++epoch.current; busy.current = true;
    setSession(null); setMessages([]); setPending(null); setDraft(''); setCanStore(false); setSending(false); setError(''); setPhase('loading');
    try {
      const s = await api.current.open(controller.signal);
      if (started !== epoch.current) return;
      setSession(s);
      let recovered = null;
      try { probeStorage(sessionStorage, s.conversation_id); recovered = readPending(sessionStorage, s.conversation_id); setPending(recovered); setCanStore(true); }
      catch { setError('浏览器会话存储不可用或待确认记录损坏，已暂停发送。'); }
      const h = await api.current.history(controller.signal);
      if (started !== epoch.current) return;
      if (!Array.isArray(h?.messages)) throw new Error('invalid history');
      setMessages(h.messages);setStatus(h.status||'bot_active');
      await confirmPending(started, s, recovered);
      if (started === epoch.current) setPhase('ready');
    } catch (e) { if (started === epoch.current) { setPhase('error'); failure(e); } }
    finally { if (started === epoch.current) busy.current = false; }
  }
  useEffect(() => {
    connect();
    const check = () => { if (api.current.changed()) resetIdentity(); };
    window.addEventListener('storage', check); window.addEventListener('focus', check);
    const visible = () => { if (!document.hidden) check(); };
    document.addEventListener('visibilitychange', visible);
    return () => { epoch.current++; lifetime.current?.abort(); api.current.clear(); window.removeEventListener('storage', check); window.removeEventListener('focus', check); document.removeEventListener('visibilitychange', visible); };
  }, []);
  async function refresh(checkReceipt = true) {
    if (busy.current || !session) return;
    busy.current = true; const started = epoch.current; setError('');
    try { const h = await api.current.history(lifetime.current.signal); if (started === epoch.current) { if (!Array.isArray(h?.messages)) throw new Error('history'); setMessages(h.messages);setStatus(h.status||'bot_active'); if (checkReceipt) await confirmPending(started, session, pending); } }
    catch (e) { if (started === epoch.current) failure(e); }
    finally { if (started === epoch.current) busy.current = false; }
  }
  const refreshRef=useRef(refresh);refreshRef.current=refresh;
  useEffect(()=>{if(phase!=='ready'||!['waiting_human','human_active'].includes(status))return;const id=setInterval(()=>{if(document.visibilityState==='visible')refreshRef.current(false);},5000);return()=>clearInterval(id);},[phase,status]);
  async function control(kind){
    if(busy.current||pending||phase!=='ready')return;
    busy.current=true;setSending(true);setError('');const started=epoch.current;
    try{
      if(kind==='handoff'){const result=await api.current.handoff(lifetime.current.signal);if(started===epoch.current)setStatus(result.status);}
      else {await api.current.nextRound(lifetime.current.signal);if(started===epoch.current)await connect();}
    }catch(e){if(started===epoch.current)failure(e);}
    finally{if(started===epoch.current){busy.current=false;setSending(false);}}
  }
  async function send(e) {
    e?.preventDefault();
    if (busy.current || !session || !canStore || phase !== 'ready' || status === 'closed' || (!pending && !validContent(draft))) return;
    if (api.current.changed()) { resetIdentity(); return; }
    const started = epoch.current; busy.current = true; setSending(true); setError('');
    let p = pending;
    try {
      if (!p) { p = { event_id: eventID(), content: draft }; savePending(sessionStorage, session.conversation_id, p); setPending(p); setDraft(''); }
      const result = await api.current.send(p, lifetime.current.signal);
      if (started !== epoch.current) return;
      if (!result?.message_id) throw new Error('unconfirmed send');
      sessionStorage.removeItem(pendingKey(session.conversation_id)); setPending(null);
      const h = await api.current.history(lifetime.current.signal);
      if (started === epoch.current) { if (!Array.isArray(h?.messages)) throw new Error('history'); setMessages(h.messages);setStatus(h.status||'bot_active'); }
    } catch (e) { if (started === epoch.current) failure(e); }
    finally { if (started === epoch.current) { busy.current = false; setSending(false); } }
  }
  return <main className="mx-auto grid min-h-[100dvh] max-w-6xl gap-7 bg-stone-50 px-4 py-6 text-stone-800 md:grid-cols-[240px_minmax(0,1fr)] md:px-8 md:py-10">
    <aside className="space-y-5"><p className="text-xs tracking-widest text-emerald-900">OMNI / CRMEB 接入验证</p><h1 className="text-2xl font-medium tracking-tight">客服测试会话</h1><p className="text-sm leading-7 text-stone-500">使用商城登录身份，连接独立客服。原人工客服入口不受影响。</p><p className="border-t border-stone-200 pt-4 text-xs leading-6 text-stone-500">{replyDescription(session?.reply_mode)}{session?.storage === 'postgres' ? '消息保存在独立测试数据库，服务重启后可恢复历史记录。' : session?.storage === 'memory' ? '当前为临时内存存储，服务重启后记录会丢失。' : '连接成功后确认消息存储方式。'}</p><a className="inline-block text-sm text-emerald-900 underline underline-offset-4" href="/pages/user/index">返回商城个人中心</a></aside>
    <section className="flex min-h-[650px] min-w-0 flex-col overflow-hidden rounded-2xl border border-stone-200 bg-white">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-stone-200 px-5 py-4"><div><h2 className="text-sm font-medium">{phase === 'ready' ? '已验证的客服会话' : phase === 'loading' ? '正在验证登录身份' : '尚未连接'}</h2><p className="mt-1 font-mono text-xs text-stone-500">{session ? `会话 · ${session.conversation_id.slice(-12)}` : '先登录商城，再连接客服'}</p></div><button className="secondary-button" disabled={sending || phase === 'loading'} onClick={session && phase === 'ready' ? refresh : connect}>{session && phase === 'ready' ? '刷新记录' : '重新连接'}</button></header>
      <div className="flex-1 space-y-4 px-5 py-6" aria-live="polite">
        {phase === 'loading' && <div role="status" aria-label="正在加载" className="space-y-4"><div className="skeleton h-12 w-3/4 rounded-xl"/><div className="skeleton ml-auto h-12 w-1/2 rounded-xl"/></div>}
        {phase === 'ready' && !messages.length && <div className="py-16"><h3 className="font-medium">这是你的独立会话</h3><p className="mt-3 text-sm leading-7 text-stone-500">发送一条测试消息，检查回复与刷新恢复。请勿输入真实个人信息。</p></div>}
        {messages.filter(m => ['customer', 'bot', 'human_agent'].includes(m.role)).map(m => <article key={m.id} className={`message-enter max-w-[90%] ${m.role === 'customer' ? 'ml-auto' : ''}`}><p className="mb-1 text-xs text-stone-500">{m.role === 'customer' ? '你' : m.role === 'bot' ? '客服回复' : '人工客服'}</p><p className={`whitespace-pre-wrap break-words rounded-xl px-4 py-3 text-sm leading-7 [overflow-wrap:anywhere] ${m.role === 'customer' ? 'bg-emerald-50' : 'bg-stone-100'}`}>{m.content}</p></article>)}
        {pending && <div className="ml-auto max-w-[90%] rounded-xl border border-dashed border-stone-300 p-4"><p className="text-xs text-stone-500">{sending ? (session?.reply_mode === 'ai' ? '正在生成回复，请稍候；不要重复发送。' : '等待确认') : '待确认 · 消息可能已保存，重试沿用同一消息标识'}</p><p className="mt-2 whitespace-pre-wrap break-words text-sm [overflow-wrap:anywhere]">{pending.content}</p></div>}
      </div>
      <footer className="border-t border-stone-200 p-5">{error && <p role="alert" className="mb-4 text-sm leading-6 text-stone-700">{error}</p>}{phase === 'unauthorized' && <a className="primary-button inline-block" href="/pages/users/login/index">前往商城登录</a>}
        {phase==='ready'&&<div className="mb-4 flex flex-wrap items-center gap-3"><p role="status" className="text-sm">{{bot_active:'智能/模拟客服',waiting_human:'等待人工接管',human_active:'人工处理中',closed:'本次咨询已结束'}[status]||'状态待刷新'}</p>{session?.human_enabled&&status==='bot_active'&&<button disabled={sending||!!pending} className="secondary-button" onClick={()=>control('handoff')}>申请人工客服</button>}{session?.new_consultation_enabled&&status==='closed'&&<button disabled={sending||!!pending||!!draft} className="primary-button" onClick={()=>control('next')}>开始新咨询</button>}{status==='closed'&&(!!draft||!!pending)&&<div className="space-y-2"><p className="text-xs">咨询已结束。待确认消息可能已保存，请先刷新记录核对；清理只移除本机草稿和待确认标记，不删除服务器消息。</p><button className="secondary-button" disabled={sending} onClick={()=>{try{sessionStorage.removeItem(pendingKey(session.conversation_id));setPending(null);setDraft('');}catch{setError('无法清理本机会话存储，请检查浏览器设置。');}}}>清理本机草稿与待确认标记</button></div>}</div>}
        {phase === 'ready' && status!=='closed' && (pending ? <button className="primary-button disabled:opacity-40" disabled={sending || !canStore} onClick={send}>{sending ? '正在等待服务确认' : '重试待确认消息'}</button> : <form onSubmit={send}><label className="mb-2 block text-xs font-medium" htmlFor="message">测试消息</label><textarea id="message" rows={3} className="w-full resize-y rounded-xl border border-stone-200 p-3 text-sm leading-6 outline-none focus:border-emerald-800" value={draft} onChange={e => setDraft(e.target.value)} disabled={sending || !canStore} placeholder="写下用于本轮验收的消息"/><div className="mt-3 flex items-center justify-between"><span className="font-mono text-xs text-stone-500">{byteLength(draft)} / 4,000 bytes</span><button type="submit" className="primary-button disabled:opacity-40" disabled={sending || !canStore || !validContent(draft)}>发送</button></div></form>)}
      </footer>
    </section>
  </main>;
}
createRoot(document.getElementById('root')).render(<Chat/>);
