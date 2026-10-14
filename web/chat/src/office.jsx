import React,{useState} from 'react';
import {createRoot} from 'react-dom/client';
import {Chat} from './app.jsx';
import {Desk} from './desk.jsx';
import {HostSessionBoundary} from './host-session.jsx';
import {createHostSessionConfig,announceHostIdentityChange} from './host-session.mjs';
import {requestJSON} from './protocol.mjs';
const staff=['/access/operator','/support/desk'].includes(location.pathname);
const realm=staff?'operator':'member',loginURL=`/access/${realm}`;
const destination=staff?'/support/desk':'/support/chat';
const config=createHostSessionConfig({role:staff?'staff':'customer',identityURL:`/support/identity/${realm}`,logoutURL:`/access/${realm}/close`,loginURL,channelName:'office-portal-auth',label:'办公服务门户',notice:'本地固定回复验收'});
function Login(){
  const [busy,setBusy]=useState(false),[error,setError]=useState('');
  async function submit(event){
    event.preventDefault();if(busy)return;
    const form=new FormData(event.currentTarget);setBusy(true);setError('');
    try{
      // The existing business login schema is intentionally not account/password.
      await requestJSON(`/access/${realm}/open`,{method:'POST',body:{handle:form.get('handle'),passphrase:form.get('passphrase')}});
      announceHostIdentityChange(config);location.assign(destination);
    }catch{setError('无法登录，请核对临时账号、口令和启用状态。');setBusy(false);}
  }
  return <main className="mx-auto grid min-h-[100dvh] max-w-6xl items-center gap-12 px-6 py-12 md:grid-cols-[1.2fr_1fr] md:gap-24">
    <section><p className="text-xs tracking-widest text-emerald-900">办公服务门户 / 独立装配验收</p><h1 className="mt-5 text-3xl font-medium tracking-tight">{staff?'服务专员登录':'成员登录'}</h1><p className="mt-6 max-w-md text-sm leading-7 text-stone-600">保留门户原有登录方式，将已验证的成员和专员身份接入通用客服。业务差异留在适配层。</p><p className="mt-8 border-t border-stone-300 pt-5 text-xs leading-6 text-stone-500">仅限本机临时账号 · 固定模拟回复<br/>不访问真实办公数据，不调用付费模型。</p><a className="mt-6 inline-block text-sm underline" href={staff?'/access/member':'/access/operator'}>{staff?'前往成员入口':'前往服务专员入口'}</a></section>
    <form aria-label="办公门户登录" onSubmit={submit} className="space-y-6 rounded-2xl border border-stone-200 bg-white p-6 md:p-9">
      <label className="grid gap-2 text-sm">门户账号<input name="handle" required autoComplete="username" disabled={busy} className="rounded-lg border border-stone-300 px-3 py-3 outline-emerald-800"/></label>
      <label className="grid gap-2 text-sm">登录口令<input name="passphrase" required type="password" autoComplete="current-password" disabled={busy} className="rounded-lg border border-stone-300 px-3 py-3 outline-emerald-800"/></label>
      <p className="text-xs leading-6 text-stone-500">使用启动器生成的临时账号。同一角色重新登录会替换当前浏览器身份。</p>
      {error&&<p role="alert" className="text-sm leading-6 text-red-800">{error}</p>}
      <button type="submit" disabled={busy} className="primary-button w-full">{busy?'正在验证…':'验证并进入'}</button>
    </form>
  </main>;
}
function Service(){return <HostSessionBoundary config={config}>{({fetcher,request,storageScope})=>staff
  ?<Desk request={request} externalLoginURL={loginURL} customerURL="/access/member" hostLabel="办公门户"/>
  :<Chat businessAuthenticated fetcher={fetcher} storageScope={storageScope}/>
}</HostSessionBoundary>;}
createRoot(document.getElementById('root')).render(location.pathname===destination?<Service/>:<Login/>);
