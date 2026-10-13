import React,{useEffect,useRef,useState} from 'react';
import {requestJSON as defaultRequest} from './protocol.mjs';
import {createReadReceipts} from './read-receipts.mjs';

export function ReadReceipts({agent,conversation,ids,enabled,root,locked,onRead,request=defaultRequest}){
 const acknowledged=useRef(onRead);acknowledged.current=onRead;
 const [failed,setFailed]=useState(false),[retry,setRetry]=useState(0);
 const idKey=JSON.stringify(ids||[]);
 useEffect(()=>{
  setFailed(false);if(!enabled||!agent||!conversation||!root.current)return;
  const controller=new AbortController();let alive=true;
  const tracker=createReadReceipts({
   active:()=>alive&&!locked.current,visible:()=>document.visibilityState==='visible',onError:()=>setFailed(true),
   send:async message_ids=>{
    const result=await request('/desk/action',{method:'POST',signal:controller.signal,body:{action:'read',conversation_id:conversation,expected_agent_id:agent,message_ids}});
    if(result.conversation_id!==conversation||result.acknowledged!==true)throw Error('invalid read acknowledgement');
    if(alive)acknowledged.current?.(message_ids);
   }
  });
  const unread=new Set(JSON.parse(idKey));
  const observer=new IntersectionObserver(entries=>{for(const entry of entries)tracker.intersection(entry.target.dataset.readMessage,entry.isIntersecting&&entry.intersectionRatio>0);});
  for(const node of root.current.querySelectorAll('[data-read-message]'))if(unread.has(node.dataset.readMessage))observer.observe(node);
  const visibility=()=>tracker.visibility();document.addEventListener('visibilitychange',visibility);
  return()=>{alive=false;controller.abort();tracker.stop();observer.disconnect();document.removeEventListener('visibilitychange',visibility);};
 },[agent,conversation,idKey,enabled,retry,root,locked,request]);
 return failed?<p role="status" className="my-3 text-xs text-stone-500">已读状态尚未保存，未读提醒会保留。<button className="ml-2 underline" onClick={()=>setRetry(v=>v+1)}>重试保存</button></p>:null;
}
