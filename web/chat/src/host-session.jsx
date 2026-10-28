import React,{useEffect,useMemo,useRef,useState} from 'react';
import {flushSync} from 'react-dom';
import {hostIdentityFailureStatus,hostSessionStatusView} from './host-session-status.mjs';
import {requestJSON} from './protocol.mjs';
import {createPinnedFetch,readHostIdentity,announceHostIdentityChange} from './host-session.mjs';
function SessionContent({config,identity,current,invalidate,children}) {
  const alive=useRef(true);
  useEffect(()=>{alive.current=true;return()=>{alive.current=false;};},[]);
  const fetcher=useMemo(()=>createPinnedFetch({version:identity.version,identity,identityURL:config.identityURL,isCurrent:()=>alive.current&&current.current?.version===identity.version&&current.current?.id===identity.id,onUnauthorized:invalidate}),[identity.id,identity.version]);
  const request=useMemo(()=>(path,options)=>requestJSON(path,{...options,fetcher}),[fetcher]);
  return children({identity,fetcher,request,storageScope:JSON.stringify([config.channelName,config.role,identity.id,identity.version])+':' });
}
export function HostSessionBoundary({config,children}){
  return <SessionBoundary key={JSON.stringify(config)} config={config}>{children}</SessionBoundary>;
}
function SessionBoundary({config,children}){
  const {role,identityURL,logoutURL,loginURL,channelName,label,notice}=config;
  const loggingOut=useRef(false);
  const [identity,setIdentity]=useState(null),[status,setStatus]=useState('checking');
  const retry=useRef(()=>{});
  const statusView=hostSessionStatusView(status);
  const current=useRef(null),generation=useRef(0),pending=useRef(null);
  const invalidate=(reason='unauthorized')=>{current.current=null;++generation.current;pending.current?.abort();setIdentity(null);setStatus(reason);};
  useEffect(()=>{
    let alive=true;
    function clear(){if(!loggingOut.current)flushSync(()=>invalidate('checking'));}
    function offline(){if(!loggingOut.current)flushSync(()=>invalidate('offline'));}
    async function check(){
      if(document.visibilityState==='hidden'||loggingOut.current||pending.current)return;
      const epoch=generation.current,controller=new AbortController();pending.current=controller;
      try{const data=await requestJSON(identityURL,{signal:controller.signal});
        if(!alive||epoch!==generation.current)return;
        const next=readHostIdentity(data,role);
        if(!next){invalidate();return;}
        current.current=next;
        setIdentity(old=>old?.version===next.version&&old.id===next.id?old:next);setStatus('');
      }catch(e){if(alive&&epoch===generation.current&&e.name!=='AbortError')invalidate(hostIdentityFailureStatus(e,navigator.onLine));}
      finally{if(pending.current===controller)pending.current=null;}
    }
    function reset(){if(loggingOut.current)return;clear();pending.current=null;void check();}
    retry.current=reset;
    function visibility(){if(document.visibilityState==='hidden')clear();else reset();}
    let channel;
    try{if(typeof BroadcastChannel==='function')channel=new BroadcastChannel(channelName);}catch{/* Polling and focus checks remain available. */}
    if(channel)channel.onmessage=e=>{if(e.data?.role===role)reset();};
    window.addEventListener('online',reset);window.addEventListener('offline',offline);window.addEventListener('focus',reset);window.addEventListener('blur',clear);window.addEventListener('pageshow',reset);window.addEventListener('pagehide',clear);document.addEventListener('visibilitychange',visibility);
    const timer=setInterval(check,3000);void check();
    return()=>{alive=false;retry.current=()=>{};window.removeEventListener('online',reset);window.removeEventListener('offline',offline);++generation.current;current.current=null;pending.current?.abort();channel?.close();clearInterval(timer);window.removeEventListener('focus',reset);window.removeEventListener('blur',clear);window.removeEventListener('pageshow',reset);window.removeEventListener('pagehide',clear);document.removeEventListener('visibilitychange',visibility);};
  },[role,identityURL,logoutURL,loginURL,channelName]);
  async function logout(){loggingOut.current=true;invalidate('signingOut');try{await requestJSON(logoutURL,{method:'POST',body:{}});announceHostIdentityChange(config);location.assign(loginURL);}catch{setStatus('logoutFailed');}}
  return <><nav aria-label="业务身份" className="flex flex-wrap items-center justify-between gap-3 border-b border-stone-200 px-6 py-3 text-xs text-stone-600"><span>{label} · {identity?identity.id:'未显示身份'}{notice&&<> · {notice}</>}</span><div className="flex gap-5"><a href={loginURL} className="underline">切换账号</a><button onClick={logout} className="underline">退出业务登录</button></div></nav>{identity?<SessionContent key={identity.id+":"+identity.version} config={config} identity={identity} current={current} invalidate={invalidate}>{children}</SessionContent>:<main className="mx-auto max-w-3xl px-6 py-16"><p role="status" className="mb-6 text-sm">{statusView.message}</p><div className="flex flex-wrap items-center gap-5">{statusView.retry&&<button type="button" className="primary-button" onClick={()=>retry.current()}>重新核对身份</button>}<a className={statusView.retry?"underline":"primary-button"} href={loginURL}>前往业务登录</a></div></main>}</>;
}
