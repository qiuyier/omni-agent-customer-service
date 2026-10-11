import {useEffect,useRef,useState} from 'react';
import {createPoller} from './polling.mjs';

export function usePolling(read,enabled,key){
 const latest=useRef(read),poller=useRef(null);latest.current=read;
 const [state,setState]=useState('paused');
 useEffect(()=>{
  if(!enabled){setState('paused');return;}
  const p=createPoller({read:task=>latest.current(task),onState:setState,
   visible:()=>document.visibilityState==='visible',
   subscribe:fn=>{document.addEventListener('visibilitychange',fn);return()=>document.removeEventListener('visibilitychange',fn);}});
  poller.current=p;return()=>{p.stop();if(poller.current===p)poller.current=null;};
 },[enabled,key]);
 return {state,invalidate:()=>poller.current?.invalidate()};
}
export const pollingLabel=state=>state==='retrying'?'自动更新暂时失败，正在降低频率重试；也可手动刷新。':state==='active'?'自动更新已开启（约5秒）；隐藏页面时暂停。':'自动更新已暂停。';
