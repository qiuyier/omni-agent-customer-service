import React from 'react';
import {publicationDiff} from './admin-diff.mjs';
const labels={title:'问题标题',content:'答案内容',source:'资料来源',keywords:'检索关键词'};
const kinds={added:'新增',modified:'修改',removed:'删除'};
function Value({entry,field}){return <p className="mt-2 whitespace-pre-wrap break-all text-sm leading-relaxed">{!entry?'（无此条目）':Array.isArray(entry[field])?entry[field].join('\n'):entry[field]}</p>;}
export function PublicationPreview({s}){
 const diff=publicationDiff(s.data);
 let reason=s.dirty?'有未保存编辑，请先保存草稿再查看待发布差异。':s.blocked?'当前状态待核对，请先核对并采用服务器草稿。':!s.data.draft?'尚无已保存草稿。':!diff?'无法读取当前发布内容，请核对服务器状态，暂不可发布。':null;
 return <section aria-label="发布前差异预览" className="mb-7 rounded-xl border border-zinc-200 bg-white p-5">
 <h2 className="font-semibold">发布前差异预览</h2>
 {reason?<p className="muted mt-3" role="status">{reason}</p>:<>
 <p className="muted mt-3">已读取快照 r{s.data.revision} · 当前发布与已保存草稿对比；不受搜索筛选影响。预览不会发布。</p>
 <p className="mt-3 text-sm">新增 {diff.added} 条 · 修改 {diff.modified} 条 · 删除 {diff.removed} 条</p>
 {diff.empty&&<p className="mt-4 border-l-2 border-amber-700 bg-amber-50 p-3 text-sm">目标草稿为空：发布将停用全部 FAQ。仍需勾选空版本确认并确认发布。</p>}
 {!diff.changes.length&&<p className="muted mt-3">没有条目内容变更。仅条目顺序或 JSON 格式变化不计入内容差异；版本编号仍可能不同。</p>}
 <div className="mt-4 max-h-[640px] overflow-auto divide-y divide-zinc-200">{diff.changes.map(change=><details key={change.id} className="py-4"><summary className="cursor-pointer break-all text-sm font-medium">{kinds[change.kind]} · {change.after?.title||change.before.title} · ID: {change.id}</summary><div className="mt-4 grid gap-5">{change.fields.map(field=><section key={field}><h3 className="text-sm font-medium">{labels[field]}</h3><div className="mt-2 grid gap-4 md:grid-cols-2"><div className="min-w-0 rounded-lg bg-zinc-50 p-3"><p className="muted">当前发布</p><Value entry={change.before} field={field}/></div><div className="min-w-0 rounded-lg bg-emerald-50 p-3"><p className="muted">待发布草稿</p><Value entry={change.after} field={field}/></div></div></section>)}</div></details>)}</div>
 </>}
 </section>;
}
