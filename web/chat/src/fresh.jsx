import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {Chat} from './app.jsx';
import {Desk} from './desk.jsx';
import {HostSessionBoundary} from './host-session.jsx';
import {createHostSessionConfig,announceHostIdentityChange} from './host-session.mjs';
import {requestJSON} from './protocol.mjs';
const role=location.pathname.startsWith('/desk')||location.pathname==='/staff-login'?'staff':'customer';
const loginURL=role==='staff'?'/staff-login':'/',destination=role==='staff'?'/desk/':'/chat/';
const config=createHostSessionConfig({role,identityURL:`/auth/${role}/me`,logoutURL:`/auth/${role}/logout`,loginURL,channelName:'fresh-onboarding-auth',label:'空白业务接入',notice:'本机模拟回复'});
function Login(){
 const [busy,setBusy]=useState(false),[error,setError]=useState('');
 async function submit(event){event.preventDefault();if(busy)return;const form=new FormData(event.currentTarget);setBusy(true);setError('');try{await requestJSON(`/auth/${role}/login`,{method:'POST',body:{account:form.get('account'),password:form.get('password')}});announceHostIdentityChange(config);location.assign(destination);}catch{setError('登录失败，请核对临时账号、密码和服务状态。');setBusy(false);}}
 return <main className="mx-auto grid min-h-[100dvh] max-w-6xl items-center gap-12 px-6 py-12 md:grid-cols-[1.2fr_1fr] md:gap-24">
  <section><p className="text-xs tracking-widest text-emerald-900">空白业务 / 按接入包装配</p><h1 className="mt-5 text-3xl font-medium tracking-tight">{role==='staff'?'员工登录':'客户登录'}</h1><p className="mt-6 max-w-md text-sm leading-7 text-stone-600">业务登录独立保留，聊天与工作台通过适配层挂载。没有订单、商品或维修流程。</p><p className="mt-8 border-t border-stone-300 pt-5 text-xs leading-6 text-stone-500">仅限本机临时账号 · 模拟回复 · 未连接业务数据库</p><a className="mt-6 inline-block text-sm underline" href={role==='staff'?'/':'/staff-login'}>{role==='staff'?'前往客户登录':'前往员工登录'}</a></section>
  <form aria-label="业务登录" onSubmit={submit} className="space-y-6 rounded-2xl border border-stone-200 bg-white p-6 md:p-9">
   <label className="grid gap-2 text-sm">账号<input name="account" required autoComplete="username" disabled={busy} className="rounded-lg border border-stone-300 px-3 py-3 outline-emerald-800"/></label>
   <label className="grid gap-2 text-sm">密码<input name="password" type="password" required autoComplete="current-password" disabled={busy} className="rounded-lg border border-stone-300 px-3 py-3 outline-emerald-800"/></label>
   <p className="text-xs leading-6 text-stone-500">客户与员工使用独立登录。重新登录会更新当前角色的身份。</p>
   {error&&<p role="alert" className="text-sm text-red-800">{error}</p>}
   <button className="primary-button w-full" disabled={busy}>{busy?'正在验证…':'登录并进入'}</button>
  </form></main>;
}
function Service(){return <HostSessionBoundary config={config}>{({fetcher,request,storageScope})=>role==='staff'?<Desk request={request} externalLoginURL={loginURL} customerURL="/" hostLabel="空白业务"/>:<Chat businessAuthenticated fetcher={fetcher} storageScope={storageScope}/>}</HostSessionBoundary>;}
createRoot(document.getElementById('root')).render(location.pathname===destination?<Service/>:<Login/>);
