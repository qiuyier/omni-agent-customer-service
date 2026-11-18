import React,{useEffect,useRef,useState} from 'react';
import {readDeskSearch,validSearchQuery} from './desk-search.mjs';
const states={waiting_human:'等待接管',human_active:'由你处理',closed:'已结束'};
export function DeskSearch({request,agentID,scope,disabled,onOpen}){
 const [query,setQuery]=useState({keyword:'',conversation_id:'',status:''});
 const [page,setPage]=useState(null),[error,setError]=useState(''),[loading,setLoading]=useState(false);
 const generation=useRef(0),flight=useRef(null);
 function invalidate(){generation.current++;flight.current?.abort();flight.current=null;}
 useEffect(()=>{invalidate();setPage(null);setLoading(false);setError('');},[scope]);
 useEffect(()=>{const clear=()=>{invalidate();setPage(null);setLoading(false);};window.addEventListener('focus',clear);return()=>{invalidate();window.removeEventListener('focus',clear);};},[]);
 function edit(key,value){invalidate();setQuery(q=>({...q,[key]:value}));setPage(null);setLoading(false);setError('');}
 async function search(after=''){
  if(disabled||flight.current)return;
  if(!validSearchQuery(query)){setError('关键词最多256 UTF-8字节，会话编号最多128字节。');return;}
  invalidate();const epoch=generation.current,c=new AbortController();flight.current=c;
  setLoading(true);setError('');setPage(null);
  try{const result=await readDeskSearch(request,agentID,{...query,after},c.signal);if(generation.current===epoch)setPage(result);}
  catch(e){if(generation.current===epoch&&e.name!=='AbortError')setError([401,403,409].includes(e.status)?'坐席身份或权限已变化，请核对登录后重新搜索。':e.status===404?'当前接入尚未支持会话搜索。':'搜索失败或超时，请缩小条件后重试。');}
  finally{if(generation.current===epoch){flight.current=null;setLoading(false);}}
 }
 return <section aria-label="会话搜索" className="mb-6 border-y border-stone-200 py-5">
  <h2 className="text-lg font-medium">查找会话</h2>
  <p className="my-2 text-xs leading-6 text-stone-500">仅搜索本业务等待接管及分配给你的会话，包含你已结束的记录。关键词区分大小写、按原文包含匹配；每页20条，按会话编号排序。打开时重新核验权限。</p>
  <form onSubmit={e=>{e.preventDefault();search();}} className="grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_180px_auto]">
   <label className="grid gap-2 text-sm">消息关键词<input value={query.keyword} maxLength={256} onChange={e=>edit('keyword',e.target.value)} className="min-w-0 rounded-lg border border-stone-300 bg-white px-3 py-2"/></label>
   <label className="grid gap-2 text-sm">会话编号（可输入部分）<input value={query.conversation_id} maxLength={128} onChange={e=>edit('conversation_id',e.target.value)} className="min-w-0 rounded-lg border border-stone-300 bg-white px-3 py-2 font-mono"/></label>
   <label className="grid gap-2 text-sm">会话状态<select value={query.status} onChange={e=>edit('status',e.target.value)} className="rounded-lg border border-stone-300 bg-white px-3 py-2"><option value="">全部可见状态</option><option value="waiting_human">等待接管</option><option value="human_active">由我处理</option><option value="closed">已结束</option></select></label>
   <button disabled={disabled||loading} className="primary-button self-end">搜索会话</button>
  </form>
  {loading&&<div role="status" className="mt-4 animate-pulse border-l-2 border-stone-300 p-3 text-sm text-stone-500">正在查找可访问的会话…</div>}
  {error&&<p role="alert" className="mt-4 text-sm text-stone-700">{error}</p>}
  {!page&&!loading&&!error&&<p className="mt-4 text-xs text-stone-500">输入条件后搜索；条件留空可分页浏览。搜索条件不保存到浏览器存储。</p>}
  {page&&<div className="mt-4"><p role="status" className="mb-2 text-sm">{page.items.length?'本页 '+page.items.length+' 条结果':'没有符合条件的可访问会话，请调整关键词或状态。'}</p><ul className="divide-y divide-stone-200">{page.items.map(row=><li key={row.conversation_id}><button disabled={disabled||loading} onClick={()=>onOpen(row)} className="flex w-full flex-wrap gap-3 py-3 text-left text-sm hover:bg-stone-100 active:scale-[0.99]"><span>{states[row.status]}</span><span className="min-w-0 break-all font-mono text-xs">{row.conversation_id}</span><span className="ml-auto underline">查看记录</span></button></li>)}</ul>{page.next&&<button disabled={disabled||loading} onClick={()=>search(page.next)} className="secondary-button mt-3">下一页</button>}<button disabled={disabled||loading} onClick={()=>search()} className="secondary-button ml-3 mt-3">重新从首页搜索</button></div>}
 </section>;
}
