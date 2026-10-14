import React,{useEffect,useMemo,useRef,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {flushSync} from 'react-dom';
import {Chat} from './app.jsx';
import {Desk} from './desk.jsx';
import {requestJSON} from './protocol.mjs';
import {createPinnedFetch} from './business-session.mjs';
const role=location.pathname.startsWith('/desk')||location.pathname==='/staff-login'?'staff':'customer';
const loginURL=role==='staff'?'/staff-login':'/';
const destination=role==='staff'?'/desk/':'/chat/';
function announce(){try{const c=new BroadcastChannel('business-auth');c.postMessage({role});c.close();}catch{/* Focus and polling are fallback checks. */}}
function Login(){
  const [busy,setBusy]=useState(false),[error,setError]=useState('');
  async function submit(e){e.preventDefault();if(busy)return;setBusy(true);setError('');
    const form=new FormData(e.currentTarget);
    try{await requestJSON(`/auth/${role}/login`,{method:'POST',body:{account:form.get('account'),password:form.get('password')}});announce();location.assign(destination);}
    catch{setError('登录失败：请检查测试账号、密码及账号是否启用。');setBusy(false);}
  }
  return <main className="mx-auto grid min-h-[100dvh] max-w-6xl items-center gap-12 px-6 py-12 md:grid-cols-[1.2fr_1fr] md:gap-24">
    <section><p className="text-xs tracking-widest text-emerald-900">维修服务 / 通用客服接入验收</p><h1 className="mt-5 text-3xl font-medium tracking-tight">{role==='staff'?'员工登录':'客户登录'}</h1><p className="mt-6 max-w-md text-sm leading-7 text-stone-600">身份由业务系统验证，聊天和人工工作台复用同一套客服能力。这里没有商城、订单或商品依赖。</p><p className="mt-8 border-t border-stone-300 pt-5 text-xs leading-6 text-stone-500">仅限本机测试 · 固定模拟回复 · 不调用付费模型<br/>临时账号由测试启动器生成，不是生产账号。</p><a className="mt-6 inline-block text-sm underline" href={role==='staff'?'/':'/staff-login'}>{role==='staff'?'切换到客户入口':'切换到员工入口'}</a></section>
    <form onSubmit={submit} className="space-y-6 rounded-2xl border border-stone-200 bg-white p-6 md:p-9" aria-label="业务系统登录">
      <label className="grid gap-2 text-sm">账号<input required name="account" autoComplete="username" disabled={busy} className="rounded-lg border border-stone-300 px-3 py-3 outline-emerald-800"/></label>
      <label className="grid gap-2 text-sm">密码<input required name="password" type="password" autoComplete="current-password" disabled={busy} className="rounded-lg border border-stone-300 px-3 py-3 outline-emerald-800"/></label>
      <p className="text-xs leading-6 text-stone-500">登录会替换本浏览器中同一角色的身份。客户和员工可同时登录。</p>
      {error&&<p role="alert" className="text-sm leading-6 text-red-800">{error}</p>}
      <button disabled={busy} className="primary-button w-full" type="submit">{busy?'正在验证账号…':'登录并进入服务'}</button>
    </form>
  </main>;
}
function SessionContent({identity,current,invalidate}){
  const alive=useRef(true);useEffect(()=>()=>{alive.current=false;},[]);
  const fetcher=useMemo(()=>createPinnedFetch({version:identity.version,isCurrent:()=>alive.current&&current.current===identity.version,onUnauthorized:invalidate}),[identity.version]);
  const request=useMemo(()=>(path,options)=>requestJSON(path,{...options,fetcher}),[fetcher]);
  return role==='staff'?<Desk request={request} externalLoginURL="/staff-login" hostLabel="业务系统"/>:<Chat businessAuthenticated fetcher={fetcher} storageScope={`business:${identity.version}:`}/>;
}
function Boundary(){
  const [identity,setIdentity]=useState(null),[status,setStatus]=useState('正在核对业务登录身份…');
  const current=useRef(null),generation=useRef(0),pending=useRef(null);
  const invalidate=()=>{current.current=null;++generation.current;pending.current?.abort();setIdentity(null);setStatus('登录已失效或身份发生变化，请重新进入。');};
  useEffect(()=>{
    let alive=true;
    function clear(){flushSync(()=>invalidate());}
    async function check(){
      if(document.visibilityState==='hidden'||pending.current)return;
      const epoch=generation.current,controller=new AbortController();pending.current=controller;
      try{const data=await requestJSON(`/auth/${role}/me`,{signal:controller.signal});
        if(!alive||epoch!==generation.current)return;
        if(!data.identity){invalidate();return;}
        current.current=data.identity.version;
        setIdentity(old=>old?.version===data.identity.version?old:data.identity);setStatus('');
      }catch(e){if(alive&&epoch===generation.current&&e.name!=='AbortError')invalidate();}
      finally{if(pending.current===controller)pending.current=null;}
    }
    function reset(){clear();pending.current=null;void check();}
    function visibility(){if(document.visibilityState==='hidden')clear();else reset();}
    const channel=typeof BroadcastChannel==='function'?new BroadcastChannel('business-auth'):null;
    if(channel)channel.onmessage=e=>{if(e.data?.role===role)reset();};
    window.addEventListener('focus',reset);window.addEventListener('blur',clear);window.addEventListener('pageshow',reset);window.addEventListener('pagehide',clear);document.addEventListener('visibilitychange',visibility);
    const timer=setInterval(check,3000);void check();
    return()=>{alive=false;++generation.current;current.current=null;pending.current?.abort();channel?.close();clearInterval(timer);window.removeEventListener('focus',reset);window.removeEventListener('blur',clear);window.removeEventListener('pageshow',reset);window.removeEventListener('pagehide',clear);document.removeEventListener('visibilitychange',visibility);};
  },[]);
  async function logout(){invalidate();try{await requestJSON(`/auth/${role}/logout`,{method:'POST',body:{}});announce();location.assign(loginURL);}catch{setStatus('退出失败，身份内容已隐藏。请重试退出或重新登录。');}}
  return <><nav aria-label="业务身份" className="flex flex-wrap items-center justify-between gap-3 border-b border-stone-200 px-6 py-3 text-xs text-stone-600"><span>本地维修服务 · {identity?identity.id:'未显示身份'} · 模拟回复</span><div className="flex gap-5"><a href={loginURL} className="underline">切换账号</a><button onClick={logout} className="underline">退出业务登录</button></div></nav>{identity?<SessionContent key={identity.version} identity={identity} current={current} invalidate={invalidate}/>:<main className="mx-auto max-w-3xl px-6 py-16"><p role="status" className="mb-6 text-sm">{status}</p><a className="primary-button" href={loginURL}>前往业务登录</a></main>}</>;
}
createRoot(document.getElementById('root')).render(location.pathname===destination?<Boundary/>:<Login/>);
