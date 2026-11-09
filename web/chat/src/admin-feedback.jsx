import React,{useState} from 'react';
const names={pending:'待处理',ignored:'已忽略',resolved:'已解决'};
export function Feedback({s,model,onImported}){
 const [filter,setFilter]=useState('pending'),[selected,setSelected]=useState(''),[faq,setFAQ]=useState('');
 const items=(s.feedback||[]).filter(i=>Date.parse(i.expires_at)>Date.now()&&i.status===filter);
 const current=items.find(i=>i.id===selected),locked=s.busy||s.checking;
 const edit=model.can('edit')&&!locked;
 const change=(status,faq_id='')=>model.loadFeedback({id:current.id,revision:current.revision,status,faq_id});
 return <section className="mb-6 rounded-xl border border-zinc-200 bg-white p-5" aria-labelledby="feedback-heading">
 <div className="flex flex-wrap items-start justify-between gap-4"><div><h2 id="feedback-heading" className="font-semibold">未命中问题</h2><p className="muted mt-2">仅收集明确开启采集后的线上知识检索未命中；试检索和批量检查不收集。空列表不代表已开启。</p></div><button disabled={locked||!model.can('read')} onClick={()=>{setSelected('');setFAQ('');model.loadFeedback();}}>{s.busy?'处理中…':'读取问题列表'}</button></div>
 <p className="muted mt-3">默认关闭 · 规则脱敏并非完全匿名 · 不关联客户或会话 · 首次采集 7 天后不可查看 · 最多 500 条。加入 FAQ 后按知识库规则保留，不再适用问题队列保留期。</p>
 {s.feedback===null?<p className="muted mt-5">尚未读取。读取仅在本页面显示，不写入浏览器持久存储。</p>:<>
 <label className="mt-5 block max-w-xs">问题状态<select value={filter} disabled={locked} onChange={e=>{setFilter(e.target.value);setSelected('');setFAQ('');}}>{Object.entries(names).map(([key,name])=><option key={key} value={key}>{name}</option>)}</select></label>
 <p className="muted mt-3" role="status">当前 {items.length} 条；计数按相同脱敏文本合并，不代表独立客户数。</p>
 {!items.length?<p className="py-6 text-sm">没有该状态下的有效问题。问题可能尚未采集、已过期或已处理。</p>:<div className="mt-4 grid gap-5 md:grid-cols-2"><nav aria-label="未命中问题列表" className="max-h-72 overflow-auto">{items.map(i=><button key={i.id} disabled={locked} aria-current={i.id===selected?'true':undefined} className={`mb-2 w-full break-words text-left ${i.id===selected?'bg-emerald-50':''}`} onClick={()=>{setSelected(i.id);setFAQ('');}}><span className="block">{i.question}</span><span className="muted">{i.count} 次 · 到期 {new Date(i.expires_at).toLocaleString()}</span></button>)}</nav>
 <div>{!current?<p className="muted">选择问题后补充 FAQ 或更新状态。</p>:<div className="grid gap-4"><p className="break-words text-sm leading-7">{current.question}</p><p className="muted">已关联 FAQ：{current.faq_id||'无'}。转为草稿不保存、不发布，也不会自动标记解决。</p>
 <button disabled={!edit||s.blocked||s.entries.length>=1000} onClick={()=>{if(model.feedbackToDraft(current))onImported();}}>转为本地 FAQ 草稿</button>
 <div className="flex flex-wrap gap-2">{current.status!=='ignored'&&<button disabled={!edit} onClick={()=>change('ignored')}>标记忽略</button>}{current.status!=='pending'&&<button disabled={!edit} onClick={()=>change('pending')}>重新待处理</button>}</div>
 <label>已发布的对应 FAQ<select value={faq} disabled={!edit} onChange={e=>setFAQ(e.target.value)}><option value="">选择已核实的 FAQ</option>{(s.data.published?.entries||[]).map(e=><option key={e.id} value={e.id}>{e.title}</option>)}</select></label><button disabled={!edit||!faq} onClick={()=>change('resolved',faq)}>关联并标记解决</button><p className="muted">请先人工确认 FAQ 能回答该问题；服务器会核对 FAQ 仍在当前发布版本中。</p></div>}</div></div>}
 </>}
 </section>;
}
