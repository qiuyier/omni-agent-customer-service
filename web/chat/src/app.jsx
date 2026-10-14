import React, { useEffect, useRef, useState } from 'react';
import { MAX_BYTES, byteLength, validContent, pendingKey, readPending, savePending, probeStorage, createChatAPI, requestJSON, errorMessage, normalizeStorage, normalizeReplyMode } from './protocol.mjs';
import {requestJSON as baseRequest} from './protocol.mjs';
import { demoPresentation } from './demo.mjs';
import {usePolling,pollingLabel} from './use-polling.jsx';

function Icon({ name, className = 'size-5', ...props }) {
  const paths = {
    arrow: <><path d="M5 12h14m-6-6 6 6-6 6" /></>,
    back: <><path d="M19 12H5m6-6-6 6 6 6" /></>,
    send: <><path d="m5 12 14-7-4 14-3-6-7-1Z" /><path d="m12 13 7-8" /></>,
    chat: <><path d="M20 11a8 8 0 0 1-8 8H5l-3 3V11a9 9 0 0 1 18 0Z" /><path d="M7 9h8M7 13h5" /></>,
    shield: <><path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6l8-3Z" /><path d="m8.5 11.5 2.5 2.5 4.5-5" /></>,
    refresh: <><path d="M20 7v5h-5M4 17v-5h5" /><path d="M19 11a7 7 0 0 0-12-5L4 9m16 6-3 3A7 7 0 0 1 5 13" /></>,
    check: <path d="m5 12 4 4L19 6" />,
    logout: <><path d="M10 4H4v16h6m4-12 4 4-4 4m-5-4h10" /></>,
    note: <><path d="M6 3h12v18H6zM9 8h6m-6 4h6m-6 4h3" /></>,
    alert: <><path d="m12 3 10 18H2L12 3Z" /><path d="M12 9v5m0 3v.1" /></>,
  };
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" className={className} aria-hidden="true" {...props}>{paths[name] || paths.chat}</svg>;
}

function Brand({ compact = false }) {
  return <a href="/" className="inline-flex items-center gap-3 text-stone-900 no-underline" aria-label="回到示例站点首页"><span className="grid size-9 place-items-center rounded-xl bg-emerald-900 text-stone-50"><Icon name="chat" className="size-5" /></span><span className="text-sm font-semibold tracking-tight">随时聊{!compact && <span className="ml-2.5 border-l border-stone-300 pl-2.5 font-normal text-stone-500">接入演示</span>}</span></a>;
}

function DemoLabel({ storage, replyMode }) {
  return <span className="inline-flex items-center gap-2 text-xs text-stone-500"><span className="size-1.5 rounded-full bg-emerald-700" />{demoPresentation(storage, replyMode).label}</span>;
}

function announceAccountChange() {
  if (!('BroadcastChannel' in window)) return;
  try {
    const channel = new BroadcastChannel('webchat-demo-account');
    channel.postMessage({ type: 'host-account-changed' });
    channel.close();
  } catch { /* Focus and page visibility checks provide the fallback. */ }
}

function InlineError({ children }) {
  return <div role="alert" className="flex gap-2.5 rounded-xl border border-stone-300 bg-stone-100 px-4 py-3 text-sm leading-relaxed text-stone-700"><Icon name="alert" className="mt-0.5 size-4 shrink-0" /><span>{children}</span></div>;
}

export function Home() {
  const [user, setUser] = useState(null);
  const [storage, setStorage] = useState(null);
  const [replyMode, setReplyMode] = useState(null);
  const [humanEnabled,setHumanEnabled]=useState(false);
  const [phase, setPhase] = useState('loading');
  const [error, setError] = useState('');
  const controller = useRef(null);
  const generation = useRef(0);
  const changing = useRef(false);
  useEffect(() => {
    const c = new AbortController(); controller.current = c;
    const version = ++generation.current;
    requestJSON('/host/me', { signal: c.signal }).then((data) => {
      if (version === generation.current) { setUser(data.user); setStorage(normalizeStorage(data.storage)); setReplyMode(normalizeReplyMode(data.reply_mode)); setHumanEnabled(data.human_enabled===true); setPhase('ready'); }
    }).catch((e) => { if (version === generation.current && e.name !== 'AbortError') { setError(errorMessage(e)); setPhase('ready'); } });
    return () => { ++generation.current; controller.current?.abort(); };
  }, []);

  async function changeAccount(account) {
    if (changing.current) return;
    changing.current = true;
    controller.current?.abort();
    const c = new AbortController(); controller.current = c;
    const version = ++generation.current;
    setPhase(account || 'logout'); setError('');
    try {
      const data = await requestJSON(account ? '/demo/login' : '/demo/logout', { method: 'POST', body: account ? { account } : {}, signal: c.signal });
      if (version === generation.current) {
        setUser(data?.user || null);
        if (account) { setStorage(normalizeStorage(data?.storage)); setReplyMode(normalizeReplyMode(data?.reply_mode)); setHumanEnabled(data.human_enabled===true); }
        announceAccountChange();
      }
    } catch (e) {
      if (version === generation.current && e.name !== 'AbortError') setError(errorMessage(e));
    } finally {
      changing.current = false;
      if (version === generation.current) setPhase('ready');
    }
  }

  const busy = phase !== 'ready';
  const demo = demoPresentation(storage, replyMode,humanEnabled);
  return <div className="min-h-[100dvh] bg-[#f6f5f1]">
    <header className="mx-auto flex max-w-7xl items-center justify-between gap-4 px-5 py-6 md:px-10"><Brand /><span className="hidden text-xs text-stone-500 sm:block">EXAMPLE / HOST SITE</span></header>
    <main className="mx-auto grid max-w-7xl grid-cols-1 gap-10 px-5 pb-16 pt-12 md:grid-cols-[1.15fr_0.85fr] md:gap-16 md:px-10 md:pb-24 md:pt-24">
      <section className="max-w-xl self-center">
        <div className="mb-7 flex items-center gap-3 text-xs font-medium tracking-[0.12em] text-emerald-900"><span className="h-px w-8 bg-emerald-800" />浏览器接入 / 最小闭环</div>
        <h1 className="text-[2.7rem] font-medium leading-[1.2] tracking-tight text-stone-900 md:text-[3.4rem]">一个入口，<br />把对话接起来。</h1>
        <p className="mt-6 max-w-md text-base leading-8 text-stone-600">这里是一张示例站点页面。选择一个演示账号，再进入独立聊天页，体验从站点登录到发送消息、查看回复的完整过程。</p>
        <div className="mt-10 flex items-start gap-3 border-t border-stone-300/70 pt-6"><Icon name="shield" className="mt-0.5 size-5 shrink-0 text-emerald-900" /><p className="max-w-sm text-sm leading-7 text-stone-500">聊天身份由站点会话换取。短期凭证仅保留在当前页面内存，不写入网址或浏览器存储。</p></div>
      </section>
      <section aria-labelledby="account-heading" className="self-center rounded-[1.75rem] border border-stone-200 bg-white p-6 shadow-[0_16px_56px_-32px_rgba(60,54,44,0.22)] md:p-9">
        <div className="mb-8 flex items-center justify-between"><span className="font-mono text-xs text-stone-400">01 / 选择身份</span><span className="rounded-full bg-stone-100 px-2.5 py-1 text-[11px] text-stone-500">仅供本地演示</span></div>
        <h2 id="account-heading" className="text-xl font-medium tracking-tight text-stone-900">以一个演示账号开始</h2>
        <p className="mt-2 text-sm leading-7 text-stone-500">两个账号拥有各自独立的会话与消息记录。</p>
        {phase === 'loading' ? <div className="mt-6 space-y-3" aria-label="正在读取登录状态" role="status"><div className="skeleton h-20 rounded-xl" /><div className="skeleton h-20 rounded-xl" /></div> : <div className="mt-6 grid gap-3">
          {[{ key: 'lin', name: '林澈', initial: '林', sub: '演示账号 A' }, { key: 'zhou', name: '周宁', initial: '周', sub: '演示账号 B' }].map((account) => <button type="button" key={account.key} onClick={() => changeAccount(account.key)} disabled={busy} className="group flex min-h-20 items-center gap-4 rounded-xl border border-stone-200 p-4 text-left transition-colors hover:border-emerald-800/40 hover:bg-emerald-50/40 active:scale-[0.99] disabled:cursor-wait disabled:opacity-60"><span className="grid size-11 shrink-0 place-items-center rounded-full bg-[#eeeee6] text-base text-stone-600">{account.initial}</span><span className="min-w-0 flex-1"><span className="block text-sm font-medium text-stone-800">{account.name}</span><span className="mt-1 block text-xs text-stone-400">{phase === account.key ? '正在切换…' : account.sub}</span></span><Icon name="arrow" className="size-4 text-stone-400 transition-transform group-hover:translate-x-0.5" /></button>)}
        </div>}
        {error && <div className="mt-4"><InlineError>{error}</InlineError></div>}
        <div className="mt-7 border-t border-stone-200 pt-5" aria-live="polite">
          <div className="mb-4 flex min-h-6 items-center justify-between gap-3"><p className="text-xs text-stone-500">{user ? <>当前身份 <strong className="ml-1 font-medium text-stone-800">{user.display_name}</strong></> : '尚未选择演示账号'}</p>{user && <button type="button" onClick={() => changeAccount(null)} disabled={busy} className="inline-flex min-h-8 items-center gap-1.5 text-xs text-stone-500 hover:text-stone-900 disabled:opacity-50"><Icon name="logout" className="size-3.5" />退出</button>}</div>
          {user && !busy ? <a href="/chat/" className="primary-button flex w-full items-center justify-between">进入聊天<Icon name="arrow" className="size-4" /></a> : <button type="button" disabled className="primary-button flex w-full items-center justify-between disabled:cursor-not-allowed disabled:bg-stone-100 disabled:text-stone-400">{busy ? '正在准备…' : '选择账号后进入聊天'}<Icon name="arrow" className="size-4" /></button>}
        </div>
      </section>
      <footer className="flex flex-col gap-3 border-t border-stone-300/70 pt-6 text-xs leading-6 text-stone-400 md:col-span-2 md:mt-8 md:flex-row md:items-center md:justify-between"><DemoLabel storage={storage} replyMode={replyMode} /><p>{demo.description}{demo.capabilities}</p></footer>
    </main>
  </div>;
}

function useChat(fetcher,storageScope) {
  const chatStorage=()=>{const storage=window.sessionStorage;return storageScope?{getItem:key=>storage.getItem(storageScope+key),setItem:(key,value)=>storage.setItem(storageScope+key,value),removeItem:key=>storage.removeItem(storageScope+key)}:storage;};
  const requestJSON=(path,options)=>baseRequest(path,{...options,fetcher});
  const [startingRound,setStartingRound]=useState(false);
  const [previous,setPrevious]=useState(null);
  const [previousLoaded,setPreviousLoaded]=useState(false);
  const round = useRef(null);
  const [requestingHuman,setRequestingHuman]=useState(false);
  const api = useRef(null);
  if (!api.current) api.current = createChatAPI(fetcher);
  const operation = useRef({ id: 0, controller: null });
  const busyRef = useRef(false);
  const identity = useRef(null);
  const identityCheck = useRef({ id: 0, controller: null });
  const [session, setSession] = useState(null);
  const [phase, setPhase] = useState('loading');
  const [messages, setMessages] = useState([]);
  const [status, setStatus] = useState('');
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [pending, setPending] = useState(null);
  const [storageAvailable, setStorageAvailable] = useState(true);
  const [identityChecking, setIdentityChecking] = useState(false);
  const [sending, setSending] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [draft, setDraft] = useState('');
  const [mustReload, setMustReload] = useState(false);
  const start = () => {
    autoUpdate.invalidate();
    operation.current.controller?.abort();
    const controller = new AbortController();
    const id = operation.current.id + 1;
    operation.current = { controller, id };
    return { signal: controller.signal, active: () => operation.current.id === id };
  };
  const applyHistory = (data, conversationID) => {
    if (data?.conversation_id !== conversationID || !Array.isArray(data.messages)) throw new Error('invalid_history');
    setMessages(data.messages); setStatus(data.status); setTruncated(Boolean(data.truncated));
  };
  const report = (e) => { setError(errorMessage(e)); if (e.code === 'account_changed' || e.code === 'unauthorized') setMustReload(true); };

  async function initialize() {
    const task = start(); busyRef.current = true;
    setPrevious(null);setPreviousLoaded(false);setStartingRound(false);round.current=null;
    setPhase('loading'); setError(''); setNotice(''); setMustReload(false); setPending(null); setMessages([]); setSession(null); setDraft(''); setSending(false); setRefreshing(false); setIdentityChecking(false);
    identity.current = null;
    api.current.clear();
    try {
      const opened = await api.current.open(task.signal);
      if (!task.active()) return;
      setSession(opened);
      round.current=opened.conversation_id;
      identity.current = opened.user.id;
      try {
        const storage = chatStorage();
        probeStorage(storage, opened.conversation_id);
        const recovered = readPending(storage, opened.conversation_id);
        setPending(recovered); setStorageAvailable(true);
        if (recovered) setNotice('发现一条待确认消息。已保留原内容；请查看记录后手动重试，页面不会自动发送。');
      } catch {
        setStorageAvailable(false);
      }
      try {
        const data = await api.current.history(task.signal);
        if (task.active()) applyHistory(data, opened.conversation_id);
      } catch (e) {
        if (task.active() && e.name !== 'AbortError') report(e);
      }
      if (task.active()) setPhase('ready');
    } catch (e) {
      if (task.active() && e.name !== 'AbortError') { report(e); setPhase(e.code === 'unauthorized' ? 'unauthorized' : 'error'); }
    } finally { if (task.active()) busyRef.current = false; }
  }

  useEffect(() => {
    initialize();
    const checkIdentity = async () => {
      if (document.visibilityState === 'hidden') { setIdentityChecking(true); return; }
      identityCheck.current.controller?.abort();
      const controller = new AbortController();
      const id = identityCheck.current.id + 1;
      identityCheck.current = { id, controller };
      setIdentityChecking(true);
      try {
        const result = await requestJSON('/host/me', { signal: controller.signal });
        if (identityCheck.current.id !== id) return;
        if (!identity.current || result?.user?.id !== identity.current) initialize();
        else setIdentityChecking(false);
      } catch (e) {
        if (identityCheck.current.id === id && e.name !== 'AbortError') initialize();
      }
    };
    const pageShow = (event) => { if (event.persisted) checkIdentity(); };
    const pageHide = () => { setIdentityChecking(true); };
    let channel;
    try {
      if ('BroadcastChannel' in window) {
        channel = new BroadcastChannel('webchat-demo-account');
        channel.onmessage = (event) => {
          if (event.data?.type !== 'host-account-changed') return;
          identityCheck.current.id++; identityCheck.current.controller?.abort();
          initialize();
        };
      }
    } catch { /* Host identity is still checked on focus, visibility, and BFCache restore. */ }
    window.addEventListener('focus', checkIdentity);
    window.addEventListener('pageshow', pageShow);
    window.addEventListener('pagehide', pageHide);
    document.addEventListener('visibilitychange', checkIdentity);
    return () => {
      operation.current.id++; operation.current.controller?.abort(); api.current.clear();
      identityCheck.current.id++; identityCheck.current.controller?.abort(); channel?.close();
      window.removeEventListener('focus', checkIdentity);
      window.removeEventListener('pageshow', pageShow);
      window.removeEventListener('pagehide', pageHide);
      document.removeEventListener('visibilitychange', checkIdentity);
    };
  }, []);

  const autoUpdate=usePolling(async task=>{
    if(busyRef.current||!session)return;
    const version=operation.current.id,owner=session.conversation_id;
    const active=()=>task.active()&&version===operation.current.id&&round.current===owner;
    try{
      const host=await requestJSON('/host/me',{signal:task.signal});
      if(!active())return;
      if(host?.user?.id!==session.user.id){await initialize();return;}
      const data=await api.current.history(task.signal);
      if(active())applyHistory(data,owner);
    }catch(e){if(active()&&(e.code==='account_changed'||e.code==='unauthorized'))report(e);throw e;}
  },phase==='ready'&&!!session&&!mustReload&&!identityChecking&&!sending&&!refreshing&&!startingRound&&!requestingHuman,session?.conversation_id);

  async function refresh() {
    if (busyRef.current || !session) return;
    busyRef.current = true;
    const task = start(); setRefreshing(true); setError('');
    try {
      const data = await api.current.history(task.signal);
      if (task.active()) { applyHistory(data, session.conversation_id); setNotice('消息记录已更新。'); }
    } catch (e) { if (task.active() && e.name !== 'AbortError') report(e); }
    finally { if (task.active()) { busyRef.current = false; setRefreshing(false); } }
  }

  async function send() {
    if (busyRef.current || !session || mustReload || status === 'closed') return;
    const value = pending?.content ?? draft;
    if (!validContent(value)) { setError('请填写消息，且不要超过 4,000 UTF-8 字节。'); return; }
    if (!storageAvailable) { setError('消息尚未发送：请允许浏览器会话存储，或更换浏览器后重新载入页面。'); return; }
    let outgoing = pending;
    if (!outgoing) {
      if (typeof crypto.randomUUID !== 'function') { setError('当前浏览器无法生成安全消息标识，请使用 localhost 或 HTTPS 访问。'); return; }
      outgoing = { event_id: crypto.randomUUID(), content: value };
      if (storageAvailable) {
        try { savePending(chatStorage(), session.conversation_id, outgoing); }
        catch { setStorageAvailable(false); setError('消息尚未发送：浏览器无法保存待确认记录，请允许会话存储后重新载入。'); return; }
      }
      setPending(outgoing); setDraft('');
    }
    busyRef.current = true;
    const task = start(); setSending(true); setError(''); setNotice('');
    try {
      const result = await api.current.send(outgoing, task.signal);
      if (!task.active()) return;
      if (result?.conversation_id !== session.conversation_id || !result.message_id || !['replied', 'waiting_human', 'superseded'].includes(result.outcome)) throw new Error('invalid_receipt');
      // Clear uncertainty before the independent history fetch. A history failure must not replay a confirmed message.
      setPending(null);
      let storageWarning = '';
      try { chatStorage().removeItem(pendingKey(session.conversation_id)); }
      catch { storageWarning = '浏览器未能清理待确认缓存，请勿在其他页面重复提交这条消息。'; setStorageAvailable(false); }
      setMessages((items) => items.some((item) => item.id === result.message_id) ? items : [...items, { id: result.message_id, role: 'customer', content: outgoing.content, created_at: new Date().toISOString() }]);
      const confirmed = result.outcome === 'waiting_human' ? (session.human_enabled?'消息已接受，交由人工处理。回复会自动更新，也可手动刷新。':'消息已接受并标记为待人工处理；此入口不提供人工转接或坐席服务。') : result.outcome === 'superseded' ? '消息已接受；回复状态已发生变化，请查看最新记录。' : '消息已发送。';
      setNotice([confirmed, storageWarning].filter(Boolean).join(' '));
      try {
        const data = await api.current.history(task.signal);
        if (task.active()) applyHistory(data, session.conversation_id);
      } catch (e) {
        if (task.active() && e.name !== 'AbortError') { setError('消息已确认发送，但最新记录暂时读取失败。请点击“刷新记录”，不需要重新发送。'); if (e.code === 'account_changed' || e.code === 'unauthorized') setMustReload(true); }
      }
    } catch (e) {
      if (task.active() && e.name !== 'AbortError') report(e);
    } finally { if (task.active()) { busyRef.current = false; setSending(false); } }
  }

  async function handoff(){
    if(!session||requestingHuman||mustReload)return;
    autoUpdate.invalidate();
    const owner=session.conversation_id;setRequestingHuman(true);
    try{const result=await api.current.handoff();if(round.current===owner&&result.conversation_id===owner){setStatus(result.status);setNotice('已申请人工处理。后续回复会自动更新；不承诺等待时间。');}}
    catch(e){if(round.current===owner)report(e);}finally{setRequestingHuman(false);}
  }
  async function nextRound(){
    if(busyRef.current||!session||status!=='closed'||mustReload||requestingHuman)return;
    busyRef.current=true;const task=start();setStartingRound(true);setError('');
    try{
      const result=await api.current.nextRound(task.signal);
      if(!task.active())return;
      if(!result?.conversation_id||result.conversation_id===session.conversation_id)throw new Error('invalid_round');
      // Explicit user action alone permits selecting a new round. Old pending
      // storage is retained under its original key, never copied or replayed.
      await initialize();
    }catch(e){if(task.active()&&e.name!=='AbortError')report(e);}
    finally{if(task.active()){busyRef.current=false;setStartingRound(false);}}
  }
  async function loadPrevious(){
    if(busyRef.current||!session||mustReload)return;
    busyRef.current=true;const task=start();setRefreshing(true);setError('');
    try{
      const data=await api.current.previousRound(task.signal);
      if(!task.active())return;
      if(data?.history!==null&&(!data?.history?.conversation_id||!Array.isArray(data.history.messages)))throw new Error('invalid_history');
      let oldPending=null;
      if(data.history){try{oldPending=readPending(chatStorage(),data.history.conversation_id);}catch{/* History remains readable if local storage is unavailable. */}}
      setPrevious(data.history?{...data.history,pending:oldPending}:null);setPreviousLoaded(true);
    }catch(e){if(task.active()&&e.name!=='AbortError')report(e);}
    finally{if(task.active()){busyRef.current=false;setRefreshing(false);}}
  }
  return { session, phase, messages, status, truncated, error, notice, pending, storageAvailable, identityChecking, sending, refreshing, draft, setDraft, mustReload, initialize, refresh, send, handoff, requestingHuman, nextRound, startingRound, loadPrevious, previous, previousLoaded, updateState:autoUpdate.state };
}

function ChatSkeleton() {
  return <div className="space-y-7 px-6 py-8 md:px-9" role="status" aria-label="正在读取会话与消息"><div className="skeleton h-3 w-32 rounded" /><div className="skeleton h-20 w-4/5 rounded-2xl" /><div className="skeleton ml-auto h-14 w-3/5 rounded-2xl" /><div className="skeleton h-24 w-4/5 rounded-2xl" /><p className="text-xs text-stone-400">正在安全连接当前账号的会话…</p></div>;
}

function Message({ message }) {
  const own = message.role === 'customer';
  const parsedTime = new Date(message.created_at);
  const time = Number.isNaN(parsedTime.getTime()) ? '' : parsedTime.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
  return <article className={`message-enter flex ${own ? 'justify-end' : 'justify-start'}`}>
    <div className={`max-w-[90%] sm:max-w-[82%] ${own ? 'text-right' : ''}`}>
      <div className={`mb-2 flex items-center gap-2 text-[11px] text-stone-400 ${own ? 'justify-end' : ''}`}><span>{own ? '你' : message.role === 'human_agent' ? '人工坐席' : '演示助手'}</span>{!own && message.role !== 'human_agent' && <span className="rounded bg-stone-100 px-1.5 py-0.5 text-[10px]">助手回复</span>}<time dateTime={message.created_at}>{time}</time></div>
      <div className={`whitespace-pre-wrap break-words rounded-2xl px-4 py-3.5 text-left text-sm leading-7 [overflow-wrap:anywhere] ${own ? 'rounded-tr-md bg-emerald-900 text-white' : 'rounded-tl-md border border-stone-200/80 bg-[#f6f6f2] text-stone-700'}`}>{message.content}</div>
      {message.content_truncated && <p className="mt-1.5 text-[10px] text-stone-400">内容较长，仅展示部分</p>}
    </div>
  </article>;
}

export function Chat({fetcher=globalThis.fetch,storageScope="",businessAuthenticated=false}={}) {
  const chat = useChat(fetcher,storageScope);
  const demo = demoPresentation(chat.session?.storage, chat.session?.reply_mode,chat.session?.human_enabled);
  if(businessAuthenticated)demo.description=demo.description.replace("模拟登录不是真实认证。","身份已由业务系统密码登录验证；本页仅供本机验收。");
  const end = useRef(null);
  const composer = useRef(null);
  const [nearBottom, setNearBottom] = useState(true);
  const bytes = byteLength(chat.draft);
  const closed = chat.status === 'closed';
  const blocked = chat.phase !== 'ready' || chat.sending || chat.refreshing || chat.startingRound || Boolean(chat.pending) || closed || chat.mustReload;
  useEffect(() => { if (nearBottom) end.current?.scrollIntoView({ block: 'end', behavior: 'instant' }); }, [chat.messages.length, chat.sending]);
  const prompt = (value) => { chat.setDraft(value); composer.current?.focus(); };
  if (chat.identityChecking) return <div className="min-h-[100dvh] bg-[#f6f5f1]"><header className="mx-auto max-w-7xl px-5 py-6 md:px-10"><Brand compact /></header><main className="mx-auto max-w-3xl px-5 py-8"><p role="status" className="mb-6 text-sm text-stone-500">正在核对当前登录身份，确认后再显示会话。</p><div className="rounded-3xl border border-stone-200 bg-white"><ChatSkeleton /></div></main></div>;
  return <div className="min-h-[100dvh] bg-[#f6f5f1]">
    <header className="mx-auto flex max-w-7xl items-center justify-between gap-4 px-4 py-5 md:px-10 md:py-6"><Brand compact /><a href="/" className="inline-flex min-h-9 items-center gap-2 text-xs text-stone-500 hover:text-stone-900"><Icon name="back" className="size-4" />返回示例站点</a></header>
    <main className="mx-auto grid max-w-7xl grid-cols-1 gap-6 px-3 pb-5 md:grid-cols-[250px_minmax(0,1fr)] md:gap-10 md:px-10 md:pb-8">
      <aside className="px-2 pt-2 md:px-0 md:pt-8">
        <p className="hidden font-mono text-[10px] tracking-[0.16em] text-stone-400 md:block">02 / INDEPENDENT CHAT</p>
        <h1 className="text-2xl font-medium tracking-tight text-stone-900 md:mt-6 md:text-[2rem] md:leading-snug">有问题，<br className="hidden md:block" />从这里开始。</h1>
        {chat.session?.new_consultation_enabled&&<div className="mt-5 space-y-3">
          {closed&&<><button type="button" className="primary-button" onClick={chat.nextRound} disabled={chat.phase!=='ready'||chat.startingRound||chat.sending||chat.refreshing||chat.requestingHuman||chat.mustReload}>{chat.startingRound?'正在建立新咨询…':'发起新咨询'}</button><p className="text-xs leading-6 text-stone-500">旧会话保持关闭。新咨询从机器人接待开始，不继承原坐席或消息。{chat.pending?'旧待确认内容保留在上一轮，不会自动发送。':''}</p></>}
          <button type="button" className="secondary-button" onClick={chat.loadPrevious} disabled={chat.phase!=='ready'||chat.startingRound||chat.sending||chat.refreshing||chat.mustReload}>{chat.refreshing?'正在读取…':'查看上一轮记录'}</button>
          {chat.previousLoaded&&!chat.previous&&<p role="status" className="text-xs leading-6 text-stone-500">还没有上一轮咨询。</p>}
          {chat.mustReload&&<button type="button" className="secondary-button" onClick={chat.initialize}>重新载入当前咨询</button>}
        </div>}
        {chat.session?.human_enabled&&<div className="mt-5"><button className="secondary-button" onClick={chat.handoff} disabled={chat.requestingHuman||chat.status!=='bot_active'||chat.mustReload}>{chat.requestingHuman?'正在申请…':chat.status==='waiting_human'?'等待人工接管':chat.status==='human_active'?'人工正在处理':closed?'会话已结束':'申请人工处理'}</button><p className="mt-2 text-xs leading-6 text-stone-500">接管后由坐席回复。页面可见时自动更新，也可手动刷新。</p></div>}
        <p className="mt-3 hidden text-sm leading-7 text-stone-500 md:block">{businessAuthenticated?'一个独立的聊天入口，沿用业务系统已验证的登录身份。':'一个独立的聊天入口，沿用你在示例站点选择的身份。'}</p>
        <div className="mt-8 hidden border-t border-stone-300/70 pt-6 md:block"><p className="mb-3 text-[11px] text-stone-400">{businessAuthenticated?'当前业务身份':'当前演示身份'}</p>{chat.session ? <div className="flex items-center gap-3"><span className="grid size-10 place-items-center rounded-full bg-[#e9e9df] text-sm text-stone-600">{chat.session.user.display_name.slice(0, 1)}</span><div><p className="text-sm font-medium text-stone-800">{chat.session.user.display_name}</p><a href="/" className="mt-1 inline-block text-xs text-stone-400 underline-offset-4 hover:underline">返回站点切换账号</a></div></div> : <p className="text-sm text-stone-500">{chat.phase === 'loading' ? '正在读取…' : '尚未连接'}</p>}</div>
        <div className="mt-10 hidden space-y-5 text-xs leading-6 text-stone-500 md:block"><div className="flex gap-2.5"><Icon name="note" className="mt-0.5 size-4 shrink-0" /><p>{demo.description}</p></div><div className="flex gap-2.5"><Icon name="shield" className="mt-0.5 size-4 shrink-0" /><p>{demo.capabilities}</p></div></div>
      </aside>
      <section aria-label="聊天窗口" className="flex min-h-[650px] min-w-0 flex-col overflow-hidden rounded-[1.5rem] border border-stone-200 bg-white shadow-[0_12px_48px_-30px_rgba(60,54,44,0.18)] md:col-start-2 md:h-[calc(100dvh-120px)] md:min-h-[660px]">
        <div className="flex shrink-0 items-center justify-between gap-3 border-b border-stone-200/80 px-5 py-4 md:px-8"><div className="flex items-center gap-3"><span className="grid size-10 place-items-center rounded-xl bg-[#f0f1e9] text-emerald-900"><Icon name="chat" className="size-5" /></span><div><h2 className="text-sm font-medium text-stone-900">演示助手</h2><div className="mt-1"><DemoLabel storage={chat.session?.storage} replyMode={chat.session?.reply_mode} /></div></div></div><button type="button" aria-label="刷新消息记录" onClick={chat.refresh} disabled={!chat.session || chat.phase !== 'ready' || chat.sending || chat.refreshing || chat.mustReload} className="inline-flex min-h-10 items-center gap-2 rounded-lg px-2 text-xs text-stone-500 hover:bg-stone-100 hover:text-stone-900 active:scale-[0.98] disabled:opacity-40"><Icon name="refresh" className="size-4" /><span className="hidden sm:inline">{chat.refreshing ? '读取中…' : '刷新记录'}</span></button></div>
        <div className="border-b border-stone-100 px-5 py-2 text-[11px] text-stone-400 md:px-8">{chat.session ? <>当前：{chat.session.user.display_name} <span className="mx-2 text-stone-300">/</span> {closed ? '会话已结束' : chat.status === 'waiting_human' ? '等待人工处理' : '独立会话'}</> : '正在建立当前账号的独立会话'}</div>
        {chat.previous&&<section aria-label="上一轮只读记录" className="shrink-0 border-b border-stone-200 bg-stone-50 px-5 py-3 md:px-8"><details open key={chat.previous.conversation_id}><summary className="cursor-pointer text-xs font-medium text-stone-700">上一轮咨询 · 只读（点击收起）</summary><p className="mt-2 text-xs leading-6 text-stone-500">旧记录不属于当前咨询，不会自动发送。{chat.previous.truncated?'仅显示最近部分记录。':''}</p><div className="mt-3 max-h-40 space-y-4 overflow-y-auto">{chat.previous.messages.length===0&&<p className="text-xs text-stone-500">上一轮没有消息。</p>}{chat.previous.messages.map(message=><Message key={message.id} message={message}/>)}{chat.previous.pending&&<div className="border-t border-stone-200 pt-3"><p className="text-xs text-stone-500">上一轮本地待确认内容（不代表已送达）</p><p className="mt-2 whitespace-pre-wrap break-words text-sm text-stone-600 [overflow-wrap:anywhere]">{chat.previous.pending.content}</p></div>}</div></details></section>}
        <p role="status" className="shrink-0 px-5 py-2 text-[11px] text-stone-500 md:px-8">{pollingLabel(chat.updateState)}</p>
        <div onScroll={(event) => { const node = event.currentTarget; setNearBottom(node.scrollHeight - node.scrollTop - node.clientHeight < 100); }} className="min-h-56 flex-1 overflow-y-auto overscroll-contain scroll-smooth motion-reduce:scroll-auto" tabIndex={0} aria-label="消息记录">
          {chat.phase === 'loading' ? <ChatSkeleton /> : !chat.session ? <div className="px-6 py-14 md:px-9"><span className="mb-5 grid size-12 place-items-center rounded-2xl bg-stone-100 text-stone-500"><Icon name="shield" /></span><h3 className="text-lg font-medium text-stone-800">{chat.phase === 'unauthorized' ? '先选择一个演示账号' : '还没有连上会话'}</h3><p className="mt-3 max-w-sm text-sm leading-7 text-stone-500">{chat.phase === 'unauthorized' ? '聊天页不会替你自动登录。请回到示例站点选择身份，再来开始对话。' : '可以重新尝试连接；这不会发送任何消息。'}</p><div className="mt-6 flex gap-3"><a href="/" className="primary-button inline-flex items-center gap-2">返回示例站点<Icon name="arrow" className="size-4" /></a>{chat.phase !== 'unauthorized' && <button type="button" onClick={chat.initialize} className="secondary-button">重新连接</button>}</div></div> : <div className="space-y-6 px-5 py-7 md:px-8" role="log" aria-label="会话消息" aria-live="polite" aria-relevant="additions text" aria-busy={chat.refreshing}>
            {chat.truncated && <p className="text-center text-[11px] leading-6 text-stone-400">仅显示最近的消息，较早记录未在此页展示。</p>}
            {chat.messages.length === 0 && !chat.pending && <div className="pb-8 pt-6 md:pt-10"><span className="mb-6 grid size-12 place-items-center rounded-2xl border border-stone-200 text-emerald-900"><Icon name="chat" className="size-6" /></span><h3 className="text-xl font-medium tracking-tight text-stone-800">你好，{chat.session.user.display_name}。</h3><p className="mt-3 max-w-md text-sm leading-7 text-stone-500">{demo.emptyDescription}</p><div className="mt-6 flex flex-wrap gap-2">{['你好，我想了解这个演示', '这个聊天页是如何工作的？'].map((value) => <button type="button" key={value} onClick={() => prompt(value)} disabled={blocked} className="rounded-full border border-stone-200 px-3.5 py-2.5 text-xs text-stone-500 transition-colors hover:border-emerald-800/30 hover:text-emerald-900 active:scale-[0.98] disabled:opacity-50">{value}</button>)}</div></div>}
            {chat.messages.map((message) => <Message key={message.id} message={message} />)}
            {chat.pending && <article className="flex justify-end"><div className="max-w-[90%] sm:max-w-[82%]"><p className="mb-2 text-right text-[11px] text-stone-400">你 · {chat.sending ? '发送中' : '等待确认'}</p><p className="whitespace-pre-wrap break-words rounded-2xl rounded-tr-md border border-dashed border-emerald-800/30 bg-emerald-50/50 px-4 py-3.5 text-sm leading-7 text-stone-600 [overflow-wrap:anywhere]">{chat.pending.content}</p></div></article>}
            {chat.sending && <div role="status" className="flex items-center gap-2 text-xs text-stone-400"><span className="sending-dot size-1.5 rounded-full bg-emerald-700" />正在发送并等待服务确认…</div>}
            <div ref={end} />
          </div>}
        </div>
        <div className="shrink-0 border-t border-stone-200/80 bg-white px-4 py-4 md:px-7">
          {chat.error && <div className="mb-3"><InlineError>{chat.error}{chat.mustReload && <a href="/" className="ml-2 underline underline-offset-4">返回站点</a>}</InlineError></div>}
          {chat.notice && <p role="status" className="mb-3 text-xs leading-6 text-stone-500">{chat.notice}</p>}
          {!chat.storageAvailable && chat.session && <div className="mb-3 rounded-xl border border-stone-200 bg-stone-50 p-3 text-xs leading-6 text-stone-600"><p>浏览器会话存储不可用，或原待确认记录无法读取。为确保刷新后可以安全重试，发送已暂停。请允许会话存储，或更换浏览器后重新载入页面。</p></div>}
          {chat.pending && !chat.sending ? <div className="flex flex-col items-start justify-between gap-3 rounded-xl border border-stone-200 bg-[#f8f8f4] p-4 sm:flex-row sm:items-center"><p className="max-w-md text-xs leading-6 text-stone-500">有一条消息尚未确认。重试会使用同一消息标识，不会创建新的发送请求标识。</p><button type="button" onClick={chat.send} disabled={chat.refreshing || chat.mustReload || closed || !chat.storageAvailable} className="primary-button shrink-0 disabled:opacity-40">重试待确认消息</button></div> : <form onSubmit={(event) => { event.preventDefault(); chat.send(); }}>
            <label htmlFor="message" className="mb-2 block text-xs font-medium text-stone-600">发送消息</label>
            <div className="rounded-xl border border-stone-200 bg-[#fcfcfa] p-3 transition-colors focus-within:border-emerald-800/60 focus-within:ring-2 focus-within:ring-emerald-800/5"><textarea ref={composer} id="message" name="message" rows={2} value={chat.draft} onChange={(event) => chat.setDraft(event.target.value)} disabled={blocked} aria-describedby="composer-help composer-count" aria-invalid={bytes > MAX_BYTES} placeholder={closed ? '当前会话已结束' : chat.sending ? '正在发送，请稍候…' : '写下你想说的…'} onKeyDown={(event) => { if (event.key === 'Enter' && (event.ctrlKey || event.metaKey) && !event.nativeEvent.isComposing) { event.preventDefault(); chat.send(); } }} className="block max-h-36 min-h-14 w-full resize-y bg-transparent text-sm leading-7 text-stone-800 outline-none placeholder:text-stone-400 disabled:cursor-not-allowed" /><div className="mt-2 flex items-center justify-between gap-3"><span id="composer-count" className={`font-mono text-[10px] ${bytes > MAX_BYTES ? 'font-semibold text-stone-900' : 'text-stone-400'}`}>{bytes.toLocaleString()} / 4,000 bytes</span><button type="submit" disabled={blocked || !validContent(chat.draft) || !chat.storageAvailable} className="primary-button inline-flex min-h-10 items-center gap-2 px-4 py-2 disabled:cursor-not-allowed disabled:bg-stone-200 disabled:text-stone-400">{chat.sending ? '发送中…' : '发送'}<Icon name="send" className="size-4" /></button></div></div>
            <p id="composer-help" className="mt-2 text-[10px] leading-5 text-stone-400">Enter 换行 · Ctrl / ⌘ + Enter 发送 · 请勿发送真实个人信息</p>
          </form>}
        </div>
      </section>
      <p className="px-2 text-[10px] leading-6 text-stone-400 md:hidden">{demo.capabilities}{demo.description}</p>
    </main>
  </div>;
}
