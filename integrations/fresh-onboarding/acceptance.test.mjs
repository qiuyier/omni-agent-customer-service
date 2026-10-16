import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {createFixture} from './fixture.mjs';
import {acceptHost} from '../host-kit/acceptance.mjs';
test('fresh business: no support -> attach -> lifecycle -> adapter restart',{timeout:180000},async t=>{
 const fixture=await createFixture();t.after(()=>fixture.close());
 const call=async(path,{body,cookie,token,version}={})=>{const r=await fetch(fixture.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:fixture.origin,...(body===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{}),...(version===undefined?{}:{'X-Business-Session':version})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(15000)});const raw=await r.text();return {status:r.status,data:raw?JSON.parse(raw):null,cookie:r.headers.get('set-cookie')?.split(';')[0]};};
 const credentials=handle=>{const a=fixture.accounts.find(a=>a.handle===handle);return {account:handle,password:a.passphrase};};
 const login=(handle,cookie)=>call(`/auth/${handle==='staff'?'staff':'customer'}/login`,{body:credentials(handle),cookie});
 let original;
 await t.test('baseline authenticates but exposes no support, including private or demo endpoints',async()=>{
  original=await login('lin');assert.equal(original.status,200);assert.ok((await call('/auth/customer/me',{cookie:original.cookie})).data.identity);
  for(const path of ['/host/me','/chat/v1/messages','/desk/queue','/chat/','/internal/chat/sessions','/demo/login'])assert.equal((await call(path)).status,404,path);
 });
 await fixture.attach();
 await t.test('public boundary rejects foreign origins, private routes, unsupported methods and oversized bodies',async()=>{
  const foreign=await fetch(fixture.origin+'/host/me',{headers:{Origin:'http://foreign.invalid'}});assert.equal(foreign.status,403);await foreign.body.cancel();
  for(const path of ['/internal/chat/sessions','/demo/login'])assert.equal((await call(path)).status,404);
  const method=await fetch(fixture.origin+'/chat/v1/messages',{method:'DELETE'});assert.equal(method.status,405);await method.body.cancel();
  assert.equal((await call('/chat/v1/messages',{body:{content:'x'.repeat(32769)}})).status,413);
 });
 await t.test('same pre-existing login works after attaching; native claims cannot choose another identity',async()=>{
  assert.equal((await call('/host/me',{cookie:original.cookie})).data.user.id,(await call('/auth/customer/me',{cookie:original.cookie})).data.identity.id);
  assert.equal((await call('/host/chat-session',{cookie:original.cookie,body:{customer_id:'foreign'}})).status,400);
  assert.equal((await call('/auth/customer/login',{body:{...credentials('lin'),role:'staff'}})).status,400);
 });
 await t.test('unmodified starter JSON paths pass real HTTP read-only and authorized lifecycle',async()=>{
  const config=JSON.parse(await readFile(new URL('../host-kit/starter/acceptance.example.json',import.meta.url),'utf8'));
  config.preflight.origin=fixture.origin;config.lifecycle.customers=['lin','zhou'].map(credentials);config.lifecycle.staff=credentials('staff');
  const read=await acceptHost(config);assert.equal(read.status,'needs_verification',JSON.stringify(read));assert.equal(read.phases.lifecycle,undefined);
  const report=await acceptHost(config,{allowTestWrites:true});assert.equal(report.phases.lifecycle?.passed,true,JSON.stringify(report));assert.equal(report.status,'needs_verification');
 });
 await t.test('logout, wrong browser version and cross-account capability fail closed',async()=>{
  const a=await login('lin');const identity=(await call('/auth/customer/me',{cookie:a.cookie})).data.identity;
  assert.equal((await call('/host/chat-session',{cookie:a.cookie,version:identity.version+'x',body:{}})).status,401);
  const cap=(await call('/host/chat-session',{cookie:a.cookie,version:identity.version,body:{}})).data;
  const b=await login('zhou',a.cookie);
  for(const cookie of [a.cookie,b.cookie])assert.equal((await call('/chat/v1/messages',{cookie,token:cap.access_token})).status,401);
  await call('/auth/customer/logout',{cookie:b.cookie,body:{}});assert.equal((await call('/host/chat-session',{cookie:b.cookie,body:{}})).status,401);
 });
 await t.test('business restart requires login; stable identity restores history and same conversation',async()=>{
  const a=await login('lin'),cap=(await call('/host/chat-session',{cookie:a.cookie,body:{}})).data;
  assert.equal((await call('/chat/v1/messages',{cookie:a.cookie,token:cap.access_token,body:{event_id:'restart-probe',content:'fresh restart probe'}})).status,200);
  await fixture.restartBusiness();assert.equal((await call('/chat/v1/messages',{cookie:a.cookie,token:cap.access_token})).status,401);
  const next=await login('lin'),newCap=(await call('/host/chat-session',{cookie:next.cookie,body:{}})).data;
  assert.equal(newCap.conversation_id,cap.conversation_id);
  const history=await call('/chat/v1/messages',{cookie:next.cookie,token:newCap.access_token});assert.equal(history.data.messages.filter(m=>m.content==='fresh restart probe').length,1);
 });
});
