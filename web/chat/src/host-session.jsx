import React,{useEffect,useMemo,useRef,useState} from 'react';
import {flushSync} from 'react-dom';
import {requestJSON} from './protocol.mjs';
import {createPinnedFetch,readHostIdentity,announceHostIdentityChange} from './host-session.mjs';
function SessionContent({config,identity,current,invalidate,children}) {
  const alive=useRef(true);
  useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
  const fetcher=useMemo(()=>createPinnedFetch({version:identity.version,isCurrent:()=>alive.current&&current.current?.version===identity.version&&current.current?.id===identity.id,onUnauthorized:invalidate}),[identity.id,identity.version]);
  const request=useMemo(()=>(path,options)=>requestJSON(path,{...options,fetcher}),[fetcher]);
  return children({identity,fetcher,request,storageScope:JSON.stringify([config.channelName,config.role,identity.id,identity.version])+':' });
}
export function HostSessionBoundary({config,children}){
  return <SessionBoundary key={JSON.stringify(config)} config={config}>{children}</SessionBoundary>;
}
function SessionBoundary({config,children}){
  const {role,identityURL,logoutURL,loginURL,channelName,label,notice}=config;
  const loggingOut=useRef(false);
  const [identity,setIdentity]=useState(null),[status,setStatus]=useState('正在核对业务登录身份…');
  const current=useRef(null),generation=useRef(0),pending=useRef(null);
  const invalidate=()=>{current.current=null;++generation.current;pending.current?.abort();setIdentity(null);setStatus('登录已失效或身份发生变化，请重新进入。');};
  useEffect(()=>{
    let alive=true;
    function clear(){flushSync(()=>invalidate());}
    async function check(){
      if(document.visibilityState==='hidden'||loggingOut.current||pending.current)return;
      const epoch=generation.current,controller=new AbortController();pending.current=controller;
      try{const data=await requestJSON(identityURL,{signal:controller.signal});
        if(!alive||epoch!==generation.current)return;
        const next=readHostIdentity(data,role);
        if(!next){invalidate();return;}
        current.current=next;
        setIdentity(old=>old?.version===next.version&&old.id===next.id?old:next);setStatus('');
      }catch(e){if(alive&&epoch===generation.current&&e.name!=='AbortError')invalidate();}
      finally{if(pending.current===controller)pending.current=null;}
    }
    function reset(){clear();pending.current=null;void check();}
    function visibility(){if(document.visibilityState==='hidden')clear();else reset();}
    let channel;
    try{if(typeof BroadcastChannel==='function')channel=new BroadcastChannel(channelName);}catch{/* Polling and focus checks remain available. */}
    if(channel)channel.onmessage=e=>{if(e.data?.role===role)reset();};
    window.addEventListener('focus',reset);window.addEventListener('blur',clear);window.addEventListener('pageshow',reset);window.addEventListener('pagehide',clear);document.addEventListener('visibilitychange',visibility);
    const timer=setInterval(check,3000);void check();
    return()=>{alive=false;++generation.current;current.current=null;pending.current?.abort();channel?.close();clearInterval(timer);window.removeEventListener('focus',reset);window.removeEventListener('blur',clear);window.removeEventListener('pageshow',reset);window.removeEventListener('pagehide',clear);document.removeEventListener('visibilitychange',visibility);};
  },[role,identityURL,logoutURL,loginURL,channelName]);
  async function logout(){loggingOut.current=true;invalidate();try{await requestJSON(logoutURL,{method:'POST',body:{}});announceHostIdentityChange(config);location.assign(loginURL);}catch{setStatus('退出失败，身份内容已隐藏。请重试退出或重新登录。');}}
  return <><nav aria-label="业务身份" className="flex flex-wrap items-center justify-between gap-3 border-b border-stone-200 px-6 py-3 text-xs text-stone-600"><span>{label} · {identity?identity.id:'未显示身份'}{notice&&<> · {notice}</>}</span><div className="flex gap-5"><a href={loginURL} className="underline">切换账号</a><button onClick={logout} className="underline">退出业务登录</button></div></nav>{identity?<SessionContent key={identity.id+":"+identity.version} config={config} identity={identity} current={current} invalidate={invalidate}>{children}</SessionContent>:<main className="mx-auto max-w-3xl px-6 py-16"><p role="status" className="mb-6 text-sm">{status}</p><a className="primary-button" href={loginURL}>前往业务登录</a></main>}</>;
}
