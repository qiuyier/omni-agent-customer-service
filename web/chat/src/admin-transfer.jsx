import React,{useEffect,useRef,useState} from 'react';
import {applyDraftImport,canImport,exportSavedDraft,importDifference,importStillCurrent,readDraftFile,templateEntries} from './admin-transfer.mjs';
export function Download({text,name,children}){
 const [url,setURL]=useState('');
 useEffect(()=>{if(text===null){setURL('');return;}const next=URL.createObjectURL(new Blob([text],{type:'application/json;charset=utf-8'}));setURL(next);return()=>URL.revokeObjectURL(next);},[text]);
 return text!==null&&url?<a className="rounded-lg border border-zinc-300 px-4 py-2 text-sm font-medium hover:bg-zinc-100 focus-visible:outline-2 focus-visible:outline-emerald-700" href={url} download={name}>{children}</a>:<span className="px-4 py-2 text-sm text-zinc-500">{children}（暂不可用）</span>;
}
export function DraftTransfer({s,model,onImported}){
 const [pending,setPending]=useState(null),[error,setError]=useState(''),[reading,setReading]=useState(false),ticket=useRef(0),input=useRef(null);
 const enabled=canImport(s),current=pending&&importStillCurrent(s,pending.base);
 useEffect(()=>()=>{ticket.current++;},[]);
 const cancel=()=>{ticket.current++;setPending(null);setError('');setReading(false);if(input.current)input.current.value='';};
 const choose=async event=>{
  const file=event.target.files?.[0];cancel();if(!file||!canImport(model.state))return;
  const base=model.state,job=++ticket.current;setReading(true);
  try{const entries=await readDraftFile(file);if(job!==ticket.current)return;if(!importStillCurrent(model.state,base))throw Error('草稿或会话已变化，请重新选择文件。');setPending({base,entries,diff:importDifference(base.entries,entries)});}
  catch(e){if(job===ticket.current)setError(e.message||'文件读取失败。');}
  finally{if(job===ticket.current)setReading(false);}
 };
 return <section aria-label="草稿导入导出" className="mb-7 rounded-xl border border-zinc-200 bg-white p-5">
 <h2 className="font-semibold">草稿导入、导出</h2><p className="muted mt-3">仅支持 UTF-8 JSON 数组，最多 1000 条、1 MiB。文件只在浏览器本地读取；确认导入不会自动保存或发布。</p>
 <div className="mt-4 flex flex-wrap gap-3"><Download text={exportSavedDraft(s)} name={'faq-draft-r'+s.data.revision+'.json'}>导出已保存草稿</Download><Download text={JSON.stringify(templateEntries,null,2)} name="faq-template.json">下载示例模板</Download></div>
 <p className="muted mt-3">导出不含未保存编辑、发布历史或账号信息。下载文件含知识内容，请妥善保管。</p>
 {s.user.permissions.includes('knowledge.edit')&&<>
 <label className="mt-5">选择 JSON 文件<input ref={input} type="file" accept=".json,application/json" disabled={!enabled||reading} onChange={choose}/></label>
 {!enabled&&<p className="muted mt-2">有未保存编辑或状态待核对时不能导入。请先保存，或明确采用服务器草稿后再操作。</p>}
 {reading&&<p role="status" className="muted mt-3">正在本地读取并校验文件…</p>}
 {error&&<p role="alert" className="mt-3 text-sm text-red-800">{error}</p>}
 {pending&&!current&&<p role="status" className="muted mt-3">导入预览已失效，请重新选择文件。</p>}
 {current&&<div className="mt-5 border-l-2 border-emerald-700 bg-emerald-50 p-4"><h3 className="font-medium">确认整份替换本地草稿？</h3><p className="mt-2 text-sm">相对已读取草稿：新增 {pending.diff.added} 条 · 修改 {pending.diff.modified} 条 · 删除 {pending.diff.removed} 条。导入后共 {pending.entries.length} 条。</p><p className="mt-2 text-sm">不是合并；文件中没有的原条目将从本地草稿移除。</p>{!pending.entries.length&&<p className="mt-3 font-medium">空数组将清空本地草稿。保存后若再发布，将停用全部 FAQ。</p>}<details className="mt-4"><summary className="cursor-pointer text-sm">查看待导入的完整内容</summary><pre className="mt-3 max-h-64 overflow-auto whitespace-pre-wrap break-all text-xs">{JSON.stringify(pending.entries,null,2)}</pre></details><button className="primary mt-4" onClick={()=>{if(applyDraftImport(model,pending.base,pending.entries)){cancel();onImported();}else {cancel();setError('草稿或会话已变化，请重新选择文件。');}}}>确认替换本地草稿</button></div>}
 {(pending||reading||error)&&<button className="mt-3" onClick={cancel}>取消导入</button>}
 </>}
 </section>;
}
