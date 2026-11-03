import React, {useEffect,useRef,useState,useSyncExternalStore} from 'react';
import {createRoot} from 'react-dom/client';
import {AdminController} from './admin.mjs';
import {matchingFAQIndices} from './admin-search.mjs';
import {PublicationPreview} from './admin-preview.jsx';
import {publicationDiff} from './admin-diff.mjs';
import {DraftTransfer} from './admin-transfer.jsx';
const model=new AdminController();
const short=v=>v?v.slice(0,12):'尚未发布';
const actionName={save_draft:'保存草稿',publish:'发布',rollback:'回退'};
function Notice({text}){return text?<p role="status" className="my-5 border-l-2 border-emerald-700 bg-emerald-50 px-4 py-3 text-sm leading-relaxed">{text}</p>:null;}
function Login({s}){
 const [account,setAccount]=useState(''),[password,setPassword]=useState('');
 return <main className="mx-auto grid min-h-[100dvh] max-w-7xl items-center gap-12 px-6 py-12 md:grid-cols-[1.3fr_1fr] md:px-12">
 <section><p className="mb-12 text-xs font-semibold tracking-[0.25em] text-emerald-800">OMNI / KNOWLEDGE</p><h1 className="text-4xl font-semibold tracking-tight">每一次回答，<br/>都有可靠的依据。</h1><p className="mt-6 max-w-md text-base leading-8 text-zinc-500">维护 FAQ 草稿，审核后发布。知识管理使用独立管理员身份，不会改变客户和坐席的权限。</p><p className="mt-12 text-xs text-zinc-500">独立入口 · 显式授权 · 操作留痕</p></section>
 <form className="grid gap-6 rounded-3xl border border-zinc-200 bg-white p-8 md:p-10" onSubmit={e=>{e.preventDefault();const secret=password;setPassword('');model.login(account,secret);}}>
 <div><h2 className="text-xl font-semibold">管理员登录</h2><p className="muted mt-2">使用部署时配置的账号，无公开注册入口。</p></div>
 <label>管理员账号<input autoComplete="username" required maxLength={64} value={account} onChange={e=>setAccount(e.target.value)} disabled={s.busy}/></label>
 <label>密码<input type="password" autoComplete="current-password" required maxLength={512} value={password} onChange={e=>setPassword(e.target.value)} disabled={s.busy}/></label>
 <button className="primary" disabled={s.busy}>{s.busy?'正在登录…':'登录知识管理'}</button><Notice text={s.notice}/><p className="muted">会话固定 30 分钟。未保存的编辑不会保存在浏览器中。</p>
 </form></main>;
}
function Events({events=[]}){return <ol className="divide-y divide-zinc-200">{[...(events||[])].reverse().slice(0,20).map(e=><li key={e.revision} className="py-3 text-sm"><div className="flex flex-wrap justify-between gap-2"><span>{actionName[e.action]||e.action} · {e.actor}</span><span className="font-mono text-xs">r{e.revision}</span></div><p className="mt-1 break-all font-mono text-xs text-zinc-500">{short(e.version)} · {new Date(e.at).toLocaleString()}</p></li>)}</ol>;}
function ConfirmAction({label,message,disabled,onConfirm,className}){
 const [open,setOpen]=useState(false),trigger=useRef(null),cancel=useRef(null),used=useRef(false);
 useEffect(()=>{if(disabled)setOpen(false);},[disabled]);
 useEffect(()=>{if(open)cancel.current?.focus();},[open]);
 const dismiss=()=>{setOpen(false);trigger.current?.focus();};
 return <div className="grid gap-3"><button ref={trigger} className={className} disabled={disabled} aria-expanded={open} onClick={()=>{used.current=false;setOpen(true);}}>{label}</button>{open&&!disabled&&<section role="region" aria-label={label+'确认'} className="rounded-lg border border-emerald-800 bg-emerald-50 p-4" onKeyDown={e=>{if(e.key==='Escape'){e.stopPropagation();dismiss();}}}><p className="break-all text-sm leading-relaxed">{message}</p><div className="mt-4 flex flex-wrap gap-2"><button ref={cancel} onClick={dismiss}>取消</button><button className="primary" onClick={()=>{if(used.current)return;used.current=true;dismiss();onConfirm();}}>确认执行</button></div></section>}</div>;
}
function Workspace({s}){
 const [query,setQuery]=useState('');
 const matches=matchingFAQIndices(s.entries,query);
 const [selected,setSelected]=useState(0),[target,setTarget]=useState(''),[allowEmpty,setAllowEmpty]=useState(false);
 const entry=s.entries[selected],locked=s.busy||s.blocked,editable=model.can('edit')&&!locked;
 const update=(key,value)=>model.edit(s.entries.map((v,i)=>i===selected?{...v,[key]:value}:v));
 useEffect(()=>{setAllowEmpty(false);},[s.data?.revision,target]);
 return <main className="mx-auto max-w-[1400px] px-4 py-8 md:px-10">
 <header className="flex flex-wrap items-center justify-between gap-5 border-b border-zinc-200 pb-6"><div><p className="mb-2 text-xs tracking-[0.2em] text-emerald-800">OMNI / KNOWLEDGE</p><h1 className="text-2xl font-semibold tracking-tight">FAQ 知识管理</h1></div><div className="flex items-center gap-4"><span className="text-sm">{s.user.id}</span><button disabled={s.busy} onClick={()=>model.logout()}>退出登录</button></div></header>
 <Notice text={s.notice}/>{!s.data?<p className="py-12">此账号无法读取知识内容。</p>:<>
 <section className="flex flex-wrap items-center justify-between gap-5 py-7"><div><h2 className="text-lg font-semibold">草稿工作区 <span className="ml-2 font-mono text-sm text-zinc-500">r{s.data.revision}</span></h2><p className="muted mt-1">{s.entries.length} 条 FAQ · {s.dirty?'有未保存编辑':'与已读取草稿一致'} · 保存草稿不会影响线上回答</p></div><div className="flex flex-wrap gap-3"><button disabled={s.busy} onClick={()=>model.inspect()}>核对服务器状态</button>{model.can('edit')&&<button className="primary" disabled={locked||!s.dirty} onClick={()=>model.mutate('draft')}>{s.busy?'处理中…':'保存草稿'}</button>}</div></section>
 {s.review&&<section className="mb-6 rounded-xl border border-emerald-800 bg-white p-5"><h3 className="font-semibold">服务器状态核对</h3><p className="muted mt-2">服务器 r{s.review.revision}；当前发布 {short(s.review.active)}；服务器草稿 {s.review.draft?.entries.length||0} 条。本地编辑暂未覆盖。</p><Events events={(s.review.events||[]).slice(-3)}/><details className="mb-4 text-sm"><summary className="cursor-pointer">查看服务器草稿内容</summary><pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-all text-xs">{JSON.stringify(s.review.draft?.entries||[],null,2)}</pre></details><ConfirmAction key={'adopt:'+s.review.revision} disabled={s.busy} label="放弃本地编辑，采用服务器草稿" message="放弃当前本地编辑，采用刚刚读取的服务器草稿？不会自动重发任何操作。" onConfirm={()=>{model.adopt();setSelected(0);}}/></section>}
 <DraftTransfer key={'transfer:'+s.user.version+':'+s.data.revision+':'+s.dirty+':'+s.blocked} s={s} model={model} onImported={()=>{setSelected(0);setQuery('');}}/>
 <PublicationPreview key={s.data.revision+':'+s.dirty+':'+s.blocked} s={s}/>
 <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_300px]"><section className="min-w-0 rounded-2xl border border-zinc-200 bg-white"><div className="flex items-center justify-between border-b border-zinc-200 p-5"><h3 className="font-medium">编辑 FAQ</h3>{model.can('edit')&&<button disabled={!editable||s.entries.length>=1000} onClick={()=>{model.edit([...s.entries,{id:'',title:'',source:'',content:'',keywords:[]}]);setSelected(s.entries.length);setQuery('');}}>新增条目</button>}</div>
 <div className="border-b border-zinc-200 p-5"><label>搜索 FAQ<input type="search" value={query} onChange={e=>setQuery(e.target.value)} placeholder="搜索标题、答案或关键词" aria-describedby="faq-search-help"/></label><div className="mt-3 flex flex-wrap items-center justify-between gap-3"><p id="faq-search-help" className="muted" role="status">匹配 {matches.length} / {s.entries.length} 条 · 仅筛选列表，保存仍包含全部条目</p><button disabled={!query} onClick={()=>setQuery('')}>清除搜索</button></div></div>
 {!s.entries.length?<div className="px-6 py-16"><h3 className="text-lg font-medium">从一条可靠的答案开始</h3><p className="muted mt-3">点击“新增条目”，填写问题标题、答案、来源和检索关键词。</p></div>:<div className="grid md:grid-cols-[200px_minmax(0,1fr)]"><nav aria-label="FAQ 条目" className="max-h-[640px] overflow-auto border-b border-zinc-200 p-3 md:border-r md:border-b-0">{!matches.length&&<p className="muted px-2 py-6">没有匹配条目。请更换搜索词或清除搜索。</p>}{matches.map(i=><button key={i} className={`mb-1 w-full border-0 text-left ${selected===i?'bg-emerald-50 text-emerald-900':''}`} aria-current={selected===i?'true':undefined} onClick={()=>setSelected(i)}><span className="mr-2 font-mono text-xs text-zinc-400">{i+1}</span>{s.entries[i].title||'未命名条目'}</button>)}</nav><div className="min-w-0 p-6">{entry&&!matches.includes(selected)&&<p className="mb-5 border-l-2 border-emerald-700 bg-emerald-50 p-3 text-sm">当前编辑条目不在搜索结果中，内容仍保留。可继续编辑，或清除搜索查看列表。</p>}{entry?<fieldset disabled={!editable} className="grid min-w-0 gap-5"><label>条目 ID<input value={entry.id} onChange={e=>update('id',e.target.value)} /><span className="muted">唯一标识，最多 128 个字符。</span></label><label>问题标题<input value={entry.title} onChange={e=>update('title',e.target.value)}/></label><label>答案内容<textarea rows={7} value={entry.content} onChange={e=>update('content',e.target.value)}/></label><label>资料来源<input value={entry.source} onChange={e=>update('source',e.target.value)}/><span className="muted">用于追溯的资料名称或标识，不会自动访问链接。</span></label><label>检索关键词<textarea rows={3} value={entry.keywords.join('\n')} onChange={e=>update('keywords',e.target.value.split('\n'))}/><span className="muted">每行一个，1–20 个，每个最多 64 字符，不留空行。</span></label><p className="muted">所有字段必填。ID、标题、来源和答案合计不超过 6000 字符；完整 FAQ 最大 1 MiB，由服务器严格校验。</p>{model.can('edit')&&<ConfirmAction key={'remove:'+selected+':'+s.data.revision} disabled={!editable} label="移除此条目" message="从本地草稿移除此条目？保存前不会影响服务器。" onConfirm={()=>{model.edit(s.entries.filter((_,i)=>i!==selected));setSelected(0);}}/>}</fieldset>:<p>请选择条目。</p>}</div></div>}
 </section><aside className="min-w-0"><section className="border-b border-zinc-200 pb-7"><h2 className="font-semibold">发布控制</h2><p className="muted mt-3">当前发布版本</p><p className="mt-2 break-all font-mono text-xs">{s.data.active||'尚未发布'}</p><p className="muted mt-4">草稿版本：{short(s.data.draft?.version)}</p>{model.can('publish')?<div className="mt-5 grid gap-4"><label className="flex items-start gap-2 font-normal"><input className="mt-1 w-4" type="checkbox" checked={allowEmpty} disabled={locked} onChange={e=>setAllowEmpty(e.target.checked)}/>允许发布或回退到空版本（停用全部 FAQ）</label><ConfirmAction key={'publish:'+s.data.revision+':'+allowEmpty} className="primary" disabled={locked||s.dirty||!publicationDiff(s.data)} label="发布已保存草稿" message="将已保存草稿发布为当前知识？此操作将影响后续回答。" onConfirm={()=>model.mutate('publish',{allow_empty:allowEmpty})}/><label>回退目标<select value={target} disabled={locked} onChange={e=>setTarget(e.target.value)}><option value="">选择历史版本</option>{s.data.versions.map(v=><option key={v} value={v}>{short(v)}{v===s.data.active?'（当前）':''}</option>)}</select></label><ConfirmAction key={'rollback:'+s.data.revision+':'+target+':'+allowEmpty} disabled={locked||s.dirty||!target||target===s.data.active} label="回退发布版本" message={`回退到 ${target}？草稿不会随之改变。`} onConfirm={()=>model.mutate('rollback',{version:target,allow_empty:allowEmpty})}/></div>:<p className="muted mt-5">此账号无发布权限，请由发布管理员审核。</p>}</section><section className="pt-7"><h2 className="font-semibold">最近操作</h2><p className="muted mt-2">最近 20 条成功操作，按时间倒序。</p><Events events={s.data.events}/>{!s.data.events?.length&&<p className="muted mt-6">保存第一份草稿后，操作记录会出现在这里。</p>}</section></aside></div></>}
 </main>;
}
function App(){
 const s=useSyncExternalStore(model.subscribe,model.snapshot);
 useEffect(()=>{
  model.open();const timer=setInterval(()=>model.verify(),30000);
  const focus=()=>model.verify(),hide=()=>{if(document.hidden)model.set({checking:true});else {model.set({checking:false});model.verify();}};
  const before=e=>{if(model.state.dirty||model.state.busy){e.preventDefault();e.returnValue='';}};
  const page=()=>model.clear('页面已恢复，请重新登录确认身份。');
  window.addEventListener('focus',focus);document.addEventListener('visibilitychange',hide);window.addEventListener('beforeunload',before);window.addEventListener('pagehide',page);
  return()=>{clearInterval(timer);window.removeEventListener('focus',focus);document.removeEventListener('visibilitychange',hide);window.removeEventListener('beforeunload',before);window.removeEventListener('pagehide',page);};
 },[]);
 if(s.checking)return <main className="mx-auto max-w-7xl px-6 py-16" aria-busy="true"><p className="mb-8 text-sm text-zinc-500">正在确认管理员身份…</p><div className="h-12 w-1/2 rounded-lg bg-zinc-200"/><div className="mt-8 h-64 rounded-xl bg-zinc-100"/></main>;
 return s.user?<Workspace s={s}/>:<Login s={s}/>;
}
createRoot(document.getElementById('root')).render(<App/>);
