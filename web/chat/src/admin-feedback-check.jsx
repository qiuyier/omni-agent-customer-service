import React from 'react';
const labels={matched:'命中对应条目',other:'命中其他条目',miss:'仍未命中'};
export function FeedbackCheck({s,model,item,expected}){
 const result=s.feedbackCheck;
 const visible=result?.id===item.id&&result.item_revision===item.revision&&result.expected_id===expected&&result.revision===s.data.revision&&result.published.version===s.data.published?.version&&Date.parse(item.expires_at)>Date.now();
 return <section className="grid gap-3 border-t border-zinc-200 pt-4" aria-label="问题检索验证">
 <button disabled={s.busy||s.checking||s.blocked||!expected||!s.data.published||!model.can('read')} onClick={()=>model.checkFeedback(item,expected)}>验证当前已发布知识</button>
 <p className="muted">先选择预期对应的 FAQ。只检索已保存的脱敏问题，检查前 3 条命中；不调用模型、不验证草稿，也不自动标记解决。</p>
 {s.busy&&<p role="status" className="muted">操作处理中…</p>}
 {visible&&<div role="status" className="grid gap-3 border-l-2 border-emerald-700 pl-4"><h3 className="font-medium">{labels[result.outcome]}</h3><p className="muted break-all">验证快照：r{result.revision} · {result.published.version.slice(0,12)}。发布或回退后需重新验证。</p>{result.published.matches.map(m=><article key={m.id} className="border-t border-zinc-200 pt-3"><p className="break-words font-medium">{m.title}</p><p className="muted break-all">ID：{m.id} · 来源：{m.source}</p><p className="whitespace-pre-wrap break-words text-sm leading-7">{m.content}</p><p className="muted">匹配关键词：{m.keywords.join('、')}</p></article>)}<p className="muted">检索命中不等于回答正确。请人工核对答案内容，再决定是否标记解决。</p></div>}
 </section>;
}
