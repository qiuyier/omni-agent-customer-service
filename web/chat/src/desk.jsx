import React,{useEffect,useLayoutEffect,useRef,useState} from 'react';
import {requestJSON as defaultRequest,validContent} from './protocol.mjs';
import {readDeskSnapshot} from './polling.mjs';
import {usePolling,pollingLabel} from './use-polling.jsx';
import {ReadReceipts} from './read-receipts.jsx';

import {mergeDeskHistory,deskTailCursor} from './desk-history.mjs';

const states={waiting_human:'等待接管',human_active:'由你处理',closed:'已结束'};
// Demo staff login is deliberately separate from customer cookies/capabilities.
// No staff or host credential enters this component or browser storage.
export function Desk({request=defaultRequest,externalLoginURL='',customerURL='/',newEventID=()=>crypto.randomUUID()}){
 const requestJSON=request;
 const [me,setMe]=useState(null),[accounts,setAccounts]=useState([]),[queue,setQueue]=useState([]),[selected,setSelected]=useState(null),[draft,setDraft]=useState(''),[pending,setPending]=useState(null),[busy,setBusy]=useState(true),[error,setError]=useState('');
 const generation=useRef(0),controller=useRef(null),locked=useRef(false),historyRoot=useRef(null),scrollRestore=useRef(null);
 const begin=()=>{autoUpdate.invalidate();controller.current?.abort();const c=new AbortController();controller.current=c;const id=++generation.current;return {signal:c.signal,active:()=>id===generation.current};};
 const autoUpdate=usePolling(async task=>{
  if(locked.current)return;const version=generation.current;
  const active=()=>task.active()&&version===generation.current;
  try{
   const data=await readDeskSnapshot(requestJSON,me?.id,selected?.conversation_id,task.signal,deskTailCursor(selected));
   if(!active())return;
   if(data.changed){generation.current++;setMe(data.me.agent);setAccounts(data.me.accounts);setQueue([]);setSelected(null);setDraft('');setPending(null);setError('坐席身份已变化，已清除旧会话显示。');return;}
   setQueue(data.queue);if(data.selected)setSelected(old=>mergeDeskHistory(old,data.selected));
  }catch(e){if(active()&&[401,403,404].includes(e.status)){generation.current++;setSelected(null);setDraft('');setPending(null);setQueue([]);if(e.status===401)setMe(null);setError('身份或会话权限已变化，请确认当前坐席后继续。');}throw e;}
 },!busy,`${me?.id||''}/${selected?.conversation_id||''}`);
 const key=(agent,conv)=>`desk-pending/v1/${agent}/${conv}`;
 async function refresh(){
  if(locked.current)return;locked.current=true;const t=begin();setBusy(true);setError('');
  try{
   const data=await readDeskSnapshot(requestJSON,me?.id,selected?.conversation_id,t.signal,deskTailCursor(selected));
   if(!t.active())return;setAccounts(data.me.accounts);setMe(data.me.agent);
   if(data.changed){
    setQueue([]);setSelected(null);setDraft('');setPending(null);
    if(data.me.agent){const q=await requestJSON('/desk/queue',{signal:t.signal});if(t.active())setQueue(q.items);}
   }else{setQueue(data.queue);if(data.selected)setSelected(old=>mergeDeskHistory(old,data.selected));}
  }catch(e){if(t.active()&&e.name!=='AbortError'){if([401,403,404].includes(e.status)){setSelected(null);setQueue([]);setDraft('');setPending(null);if(e.status===401)setMe(null);}setError('无法读取坐席信息，请刷新重试。');}}
  finally{if(t.active()){locked.current=false;setBusy(false);}}
 }
 const refreshRef=useRef(refresh);refreshRef.current=refresh;
 useEffect(()=>{refreshRef.current();const focus=()=>refreshRef.current();window.addEventListener('focus',focus);return()=>{generation.current++;controller.current?.abort();window.removeEventListener('focus',focus);};},[]);
 async function login(account){if(locked.current)return;locked.current=true;const t=begin();setBusy(true);setSelected(null);setMe(null);setQueue([]);setDraft('');setPending(null);setError('');try{await requestJSON(account?'/desk/login':'/desk/logout',{method:'POST',body:account?{account}:{},signal:t.signal});}catch{if(t.active())setError('登录操作失败，请重试。');}finally{if(t.active()){locked.current=false;setBusy(false);await refresh();}}}
 async function action(row,action){
  if(action==='history'&&selected?.conversation_id===row.conversation_id){await refresh();return;}
  if(locked.current||!me)return;locked.current=true;const t=begin();setBusy(true);setError('');
  try{
   let body={conversation_id:row.conversation_id,action,revision:row.revision};
   if(action==='reply'){
    const saved=pending||{event_id:newEventID(),content:draft};if(!validContent(saved.content))throw Error('invalid draft');
    sessionStorage.setItem(key(me.id,row.conversation_id),JSON.stringify(saved));setPending(saved);body={...body,...saved};
   }
   let result=await requestJSON('/desk/action',{method:'POST',body,signal:t.signal});if(!t.active())return;
   if(action==='reply'){sessionStorage.removeItem(key(me.id,row.conversation_id));setPending(null);setDraft('');}
   if(action!=='history')result=await requestJSON('/desk/action',{method:'POST',body:{conversation_id:row.conversation_id,action:'history'},signal:t.signal});
   if(!t.active())return;setSelected(result);
   const raw=sessionStorage.getItem(key(me.id,row.conversation_id));let saved=null;if(raw){saved=JSON.parse(raw);if(typeof saved.event_id!=='string'||!validContent(saved.content))throw Error('invalid saved draft');}setPending(saved);setDraft('');
   const q=await requestJSON('/desk/queue',{signal:t.signal});if(t.active())setQueue(q.items);
  }catch(e){if(t.active()&&e.name!=='AbortError'){setSelected(null);setError(e.status===401?'坐席登录已失效，请重新登录。':e.status===403||e.status===409?'会话已被其他坐席接管，或状态发生变化。请刷新队列。':'操作未确认，请刷新后打开原会话。待确认回复会保留原编号，请勿重新编写发送。');}}
  finally{if(t.active()){locked.current=false;setBusy(false);}}
 }

 useLayoutEffect(()=>{
  const restore=scrollRestore.current,root=historyRoot.current;if(!restore||!root)return;scrollRestore.current=null;
  if(restore.type==='prepend'){
   const node=[...root.querySelectorAll('[data-message-id]')].find(n=>n.dataset.messageId===restore.id);
   if(node)root.scrollTop+=node.getBoundingClientRect().top-root.getBoundingClientRect().top-restore.offset;
  }else{
   root.scrollTop=0;
   const node=[...root.querySelectorAll('[data-message-id]')].find(n=>n.dataset.messageId===restore.id);
   node?.focus({preventScroll:true});node?.scrollIntoView({block:'nearest'});
  }
 },[selected]);
 async function pageHistory(row,direction){
  if(locked.current||!me)return;
  const same=selected?.conversation_id===row.conversation_id;
  const cursor=direction==='before'?{before_message_id:selected?.messages?.[0]?.id}:direction==='unread'?{from_message_id:row.first_unread_id}:deskTailCursor(selected);
  if(!Object.values(cursor)[0])return;
  locked.current=true;const t=begin();setBusy(true);setError('');
  try{
   const data=await readDeskSnapshot(requestJSON,me.id,row.conversation_id,t.signal,cursor);
   if(!t.active())return;
   if(data.changed){setMe(data.me.agent);setAccounts(data.me.accounts);setQueue([]);setSelected(null);setDraft('');setPending(null);setError('坐席身份已变化，请重新选择会话。');return;}
   if(direction==='before'){
    const root=historyRoot.current;
    const node=[...(root?.querySelectorAll('[data-message-id]')||[])].find(n=>n.getBoundingClientRect().bottom>root.getBoundingClientRect().top);
    if(node)scrollRestore.current={type:'prepend',id:node.dataset.messageId,offset:node.getBoundingClientRect().top-root.getBoundingClientRect().top};
   }else if(direction==='unread')scrollRestore.current={type:'focus',id:row.first_unread_id};
   let restored=null;
   if(!same){const raw=sessionStorage.getItem(key(me.id,row.conversation_id));if(raw){restored=JSON.parse(raw);if(typeof restored?.event_id!=='string'||!validContent(restored?.content))throw Error('invalid saved draft');}}
   setSelected(old=>direction==='unread'?data.selected:mergeDeskHistory(old,data.selected,direction));
   setQueue(data.queue);
   if(!same){setDraft('');setPending(restored);}
  }catch(e){if(t.active()&&e.name!=='AbortError'){
   if([401,403,404].includes(e.status)){setSelected(null);setQueue([]);setDraft('');setPending(null);if(e.status===401)setMe(null);}
   scrollRestore.current=null;
   setError([401,403,404].includes(e.status)?'身份或会话权限已变化，请重新登录或选择会话。':'历史读取失败，请重试；当前草稿已保留。');
  }}finally{if(t.active()){locked.current=false;setBusy(false);}}
 }

 return <div className="min-h-[100dvh] bg-[#f6f5f1] text-stone-800"><header className="mx-auto flex max-w-7xl items-center justify-between px-5 py-6"><div><p className="text-xs tracking-widest text-stone-500">人工服务 / 本地接入验收</p><h1 className="mt-2 text-2xl font-medium tracking-tight">接管工作台</h1></div><a href={customerURL} className="text-sm underline">客户入口</a></header><main className="mx-auto max-w-7xl px-5 pb-10"><p className="mb-6 max-w-2xl text-sm leading-7 text-stone-500">{externalLoginURL?'使用商城已验证的客服身份，原客服入口保持不变。':'模拟坐席选择不是真实认证，不可直接上线。'}队列展示等待人工及由你处理的会话，最多100条；可见时自动更新，也可手动刷新。关闭会话后不能继续发送。</p><div className="mb-6 flex flex-wrap gap-3">{me?<><span className="py-2 text-sm">当前坐席：{me.name}</span>{externalLoginURL?<a className="secondary-button" href={externalLoginURL}>管理客服登录</a>:<button disabled={busy} onClick={()=>login(null)} className="secondary-button">退出坐席</button>}</>:accounts.map(a=><button key={a.id} disabled={busy} onClick={()=>login(a.id)} className="primary-button">以{a.name}登录</button>)}{!me&&externalLoginURL&&<a href={externalLoginURL} className="primary-button">前往客服登录</a>}<button disabled={busy} onClick={refresh} className="secondary-button">刷新队列</button></div><p role="status" className="mb-4 text-xs text-stone-500">{pollingLabel(autoUpdate.state)}</p>{error&&<p role="alert" className="mb-5 rounded-xl border border-stone-300 bg-stone-100 p-4 text-sm">{error}</p>}{busy&&<p role="status" className="mb-4 animate-pulse text-sm text-stone-500">正在核对会话状态…</p>}<div className="grid gap-6 md:grid-cols-[280px_minmax(0,1fr)]"><aside className="divide-y divide-stone-200 border-y border-stone-200">{queue.length?queue.map(row=><div key={row.conversation_id}><button disabled={busy} key={row.conversation_id} onClick={()=>action(row,'history')} aria-current={selected?.conversation_id===row.conversation_id?'true':undefined} className={`block w-full px-2 py-5 text-left hover:bg-stone-100 active:scale-[0.98] ${selected?.conversation_id===row.conversation_id?'bg-stone-100 border-l-2 border-stone-600':''}`}><span className="flex items-center justify-between gap-2"><span className="text-sm">{states[row.status]}</span>{row.attention_enabled&&row.unread_count>0&&<span aria-label={`${row.unread_count}条未读客户消息`} className="rounded-full bg-stone-700 px-2 py-1 font-mono text-xs text-white">{row.unread_count} 未读</span>}</span><span className="mt-2 block break-all font-mono text-xs text-stone-500">{row.conversation_id.slice(-16)}</span>{row.latest_message&&<span className="mt-2 line-clamp-2 break-words text-xs leading-5 text-stone-500">{row.latest_message.role==='customer'?'用户':row.latest_message.role==='human_agent'?'坐席':'助手'}：{row.latest_message.content}{row.latest_message.content_truncated?'…':''}</span>}</button>{row.first_unread_id&&<button disabled={busy} onClick={()=>pageHistory(row,'unread')} className="mb-3 px-2 text-xs underline">定位未读消息</button>}</div>):<p className="py-8 text-sm text-stone-500">{me?'暂无待处理会话。用户申请人工后将出现在这里。':(externalLoginURL?'请先在原客服入口登录，再刷新此页。':'先选择一个模拟坐席。')}</p>}</aside><section className="min-w-0 rounded-2xl border border-stone-200 bg-white p-5 md:p-8">{selected?<><h2 className="text-lg font-medium">{states[selected.status]}</h2><p className="mt-2 break-all font-mono text-xs text-stone-400">{selected.conversation_id}</p><ReadReceipts request={requestJSON} agent={me?.id} conversation={selected.conversation_id} ids={selected.unread_message_ids} enabled={!busy&&selected.attention_enabled} root={historyRoot} locked={locked} onRead={ids=>setSelected(old=>old?.conversation_id===selected.conversation_id?{...old,unread_message_ids:(old.unread_message_ids||[]).filter(id=>!ids.includes(id))}:old)}/><div className="mt-4 flex flex-wrap gap-3">{selected.has_older&&<button disabled={busy} onClick={()=>pageHistory(selected,'before')} className="secondary-button">加载更早消息</button>}{selected.first_unread_id&&<button disabled={busy} onClick={()=>pageHistory(selected,'unread')} className="secondary-button">定位未读消息</button>}{selected.has_newer&&<button disabled={busy} onClick={()=>pageHistory(selected,'after')} className="secondary-button">加载后续消息</button>}</div><div ref={historyRoot} className="my-6 max-h-[480px] space-y-5 overflow-y-auto [overflow-anchor:none]" role="log" aria-label="人工会话记录">{selected.messages?.length?selected.messages.map(m=><article key={m.id} tabIndex={-1} data-message-id={m.id} className="focus:outline-stone-400" data-read-message={m.role==='customer'?m.id:undefined}><p className="mb-1 text-xs text-stone-400">{m.role==='customer'?'用户':m.role==='human_agent'?'人工坐席':'助手'}</p><p className="whitespace-pre-wrap break-words text-sm leading-7">{m.content}</p>{m.content_truncated&&<p className="text-xs text-stone-500">超长消息仅展示片段，不会自动标记已读。</p>}</article>):<p className="text-sm text-stone-500">用户已申请人工，尚无消息。</p>}</div>{selected.status==='waiting_human'?<button disabled={busy} onClick={()=>action(selected,'claim')} className="primary-button">接管此会话</button>:selected.status==='human_active'?<><label htmlFor="desk-reply" className="mb-2 block text-sm">人工回复</label><textarea id="desk-reply" rows="3" disabled={busy||!!pending} value={pending?.content??draft} onChange={e=>setDraft(e.target.value)} className="w-full rounded-xl border border-stone-300 p-3 text-sm"/><p className="my-2 text-xs text-stone-500">最多4000 UTF-8字节；待确认消息重试沿用原编号。</p><div className="flex flex-wrap gap-3"><button disabled={busy||(!pending&&!validContent(draft))} onClick={()=>action(selected,'reply')} className="primary-button">{pending?'重试原回复':'发送人工回复'}</button><button disabled={busy||!!pending} onClick={()=>action(selected,'close')} className="secondary-button">结束并关闭会话</button></div></>:<p className="text-sm text-stone-500">处理已结束，历史保留。</p>}</>:<p className="py-10 text-sm leading-7 text-stone-500">选择左侧会话查看历史，再明确接管。未接管前不能发送人工回复。</p>}</section></div></main></div>;
}
