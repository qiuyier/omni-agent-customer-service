import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {Chat} from './app.jsx';
import {Desk} from './desk.jsx';
import {requestJSON} from './protocol.mjs';
import {HostSessionBoundary} from './host-session.jsx';
import {createHostSessionConfig,announceHostIdentityChange} from './host-session.mjs';
const role=location.pathname.startsWith('/desk')||location.pathname==='/staff-login'?'staff':'customer';
const loginURL=role==='staff'?'/staff-login':'/';
const destination=role==='staff'?'/desk/':'/chat/';
const sessionConfig=createHostSessionConfig({role,identityURL:`/auth/${role}/me`,logoutURL:`/auth/${role}/logout`,loginURL,channelName:'business-auth',label:'本地维修服务',notice:'模拟回复'});
function Login(){
  const [busy,setBusy]=useState(false),[error,setError]=useState('');
  async function submit(e){e.preventDefault();if(busy)return;setBusy(true);setError('');
    const form=new FormData(e.currentTarget);
    try{await requestJSON(`/auth/${role}/login`,{method:'POST',body:{account:form.get('account'),password:form.get('password')}});announceHostIdentityChange(sessionConfig);location.assign(destination);}
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
function Service(){
  return <HostSessionBoundary config={sessionConfig}>{({fetcher,request,storageScope})=>role==='staff'
    ?<Desk request={request} externalLoginURL={loginURL} customerURL="/" hostLabel="业务系统"/>
    :<Chat businessAuthenticated fetcher={fetcher} storageScope={storageScope}/>
  }</HostSessionBoundary>;
}
createRoot(document.getElementById('root')).render(location.pathname===destination?<Service/>:<Login/>);
