import test from 'node:test';
import assert from 'node:assert/strict';
import {randomBytes} from 'node:crypto';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {startBusiness} from './business.mjs';
import {acceptHost} from '../host-kit/acceptance.mjs';
async function verifyHost({origin,...lifecycle},options){
 const report=await acceptHost({preflight:{origin,paths:{customerIdentity:'/auth/customer/me',staffIdentity:'/auth/staff/me'}},lifecycle},options);
 assert.equal(report.status,'needs_verification',JSON.stringify(report));
 assert.ok(!report.phases.preflight.checks.some(c=>c.status==='fail'),JSON.stringify(report));
 assert.equal(report.checks.find(c=>c.id==='lifecycle').status,'pass',JSON.stringify(report));
 return report.phases.lifecycle;
}
const root=fileURLToPath(new URL('../../',import.meta.url));
const random=()=>randomBytes(32).toString('base64url');
test('password-backed business sessions integrate without demoAccounts or adapter/core changes',{timeout:180000},async t=>{
 const dir=await mkdtemp(join(tmpdir(),'omni-business-login-'));
 let gateway,business,closed;
 t.after(async()=>{await business?.close();if(gateway&&gateway.exitCode===null&&gateway.signalCode===null){gateway.kill('SIGTERM');const timer=setTimeout(()=>gateway.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}await rm(dir,{recursive:true,force:true});});
 const binary=join(dir,'gateway');
 await promisify(execFile)('go',['build','-o',binary,'./integrations/cross-system/gateway'],{cwd:root,timeout:120000});
 const hostSecret=random(),agentSecret=random();
 gateway=spawn(binary,[],{env:{FIXTURE_HOST_A:random(),FIXTURE_AGENT_A:random(),FIXTURE_HOST_B:hostSecret,FIXTURE_AGENT_B:agentSecret},stdio:['ignore','pipe','pipe']});
 closed=new Promise(r=>gateway.once('close',r));gateway.stderr.resume();
 const gatewayOrigin=await new Promise((resolve,reject)=>{
  let output='';const timer=setTimeout(()=>reject(Error('gateway startup timeout')),10000);
  const fail=()=>{clearTimeout(timer);reject(Error('gateway failed'));};gateway.once('error',fail);gateway.once('exit',fail);
  gateway.stdout.on('data',c=>{output+=c;const m=output.match(/^(http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});
 });
 const passwords={lin:random(),zhou:random(),staff:random(),outsider:random()};
 const accounts=[{role:'customer',account:'lin',id:'repair-member-1',password:passwords.lin},{role:'customer',account:'zhou',id:'repair-member-2',password:passwords.zhou},{role:'staff',account:'staff',id:'staff-1',password:passwords.staff},{role:'staff',account:'outsider',id:'unmapped-employee',password:passwords.outsider}];
 let now=Date.now();
 business=await startBusiness({gatewayOrigin,hostSecret,agents:[{id:'staff-1',name:'维修服务员工',secret:agentSecret}],accounts,now:()=>now});
 async function call(path,{body,cookie,token,version,origin=business.origin}={}){
  const r=await fetch(business.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:origin,...(version?{"X-Business-Session":version}:{}),...(body===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(10000)});
  const raw=await r.text();return {status:r.status,data:raw?JSON.parse(raw):null,cookie:r.headers.get('set-cookie')?.split(';')[0],cookieHeader:r.headers.get('set-cookie')};
 }
 const login=(role,account,cookie)=>call('/auth/'+role+'/login',{body:{account,password:passwords[account]},cookie});
 const session=cookie=>call('/host/chat-session',{body:{},cookie});
 await t.test('no demo login, wrong passwords and forged role/identity are rejected',async()=>{
  assert.equal((await call('/demo/login',{body:{account:'lin'}})).status,404);
  assert.equal((await call('/desk/login',{body:{account:'staff-1'}})).status,404);
  assert.equal((await call('/auth/customer/login',{body:{account:'lin',password:'wrong'}})).status,401);
  assert.equal((await call('/auth/staff/login',{body:{account:'lin',password:passwords.lin}})).status,401);
  assert.equal((await call('/auth/customer/login',{body:{account:'lin',password:passwords.lin,role:'staff'}})).status,400);
  assert.equal((await call('/auth/customer/login',{body:{account:'lin',password:passwords.lin},origin:'http://foreign.invalid'})).status,403);
  assert.equal((await call('/internal/chat/sessions',{body:{}})).status,404);
  assert.equal((await call('/host/me')).data.demo,false);
 });
 await t.test('configured generic verifier completes the password-authenticated human lifecycle',async()=>{
  const report=await verifyHost({origin:business.origin,customers:[{account:'lin',password:passwords.lin},{account:'zhou',password:passwords.zhou}],staff:{account:'staff',password:passwords.staff},paths:{customerLogin:'/auth/customer/login',customerLogout:'/auth/customer/logout',staffLogin:'/auth/staff/login',staffLogout:'/auth/staff/logout'}},{allowTestWrites:true});
  assert.equal(report.passed,true,JSON.stringify(report));
  for(const k of ['claim','reply','read','close','next_round','customer_logout','staff_logout'])assert.ok(report.checks.includes(k));
  for(const value of [...Object.values(passwords),hostSecret,agentSecret])assert.ok(!JSON.stringify(report).includes(value));
 });
 await t.test('customer switch revokes the old login; new login cannot reuse old capability',async()=>{
  const a=await login('customer','lin');assert.equal(a.status,200);assert.match(a.cookieHeader,/HttpOnly/);assert.match(a.cookieHeader,/SameSite=Strict/);
  const cap=await session(a.cookie);assert.equal(cap.status,200);
  const b=await login('customer','zhou',a.cookie);assert.equal(b.status,200);
  assert.equal((await call('/chat/v1/messages',{cookie:a.cookie,token:cap.data.access_token})).status,401);
  assert.equal((await call('/chat/v1/messages',{cookie:b.cookie,token:cap.data.access_token})).status,401);
  for(const cookie of [a.cookie,b.cookie])assert.equal((await call('/chat/v1/messages',{cookie,token:cap.data.access_token,body:{event_id:random(),content:'must reject stale login'}})).status,401);
  const own=await session(b.cookie);assert.equal(own.status,200);assert.notEqual(own.data.conversation_id,cap.data.conversation_id);
  assert.equal((await call('/desk/queue',{cookie:b.cookie})).status,401);
 });
 await t.test('customer disable revokes old sessions permanently, re-enable needs a new login',async()=>{
  const a=await login('customer','lin');const cap=await session(a.cookie);
  business.setEnabled('customer','lin',false);
  assert.equal((await call('/chat/v1/messages',{cookie:a.cookie,token:cap.data.access_token,body:{event_id:random(),content:'must reject disabled login'}})).status,401);
  assert.equal((await call('/chat/v1/messages',{cookie:a.cookie,token:cap.data.access_token})).status,401);
  assert.equal((await login('customer','lin')).status,401);
  business.setEnabled('customer','lin',true);
  assert.equal((await session(a.cookie)).status,401);
  const fresh=await login('customer','lin');assert.equal((await session(fresh.cookie)).status,200);
  assert.equal((await call('/chat/v1/messages',{cookie:fresh.cookie,token:cap.data.access_token})).status,401);
 });
 await t.test('staff disable, re-enable and switching to an unassigned employee do not preserve desk access',async()=>{
  const staff=await login('staff','staff');assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,200);
  assert.equal((await session(staff.cookie)).status,401);
  business.setEnabled('staff','staff',false);
  assert.equal((await call('/desk/action',{cookie:staff.cookie,body:{action:'claim',expected_agent_id:'staff-1',conversation_id:'forged'}})).status,401);
  assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,401);
  assert.equal((await login('staff','staff')).status,401);
  business.setEnabled('staff','staff',true);
  assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,401);
  const fresh=await login('staff','staff');assert.equal((await call('/desk/queue',{cookie:fresh.cookie})).status,200);
  const other=await login('staff','outsider',fresh.cookie);assert.equal(other.status,200);
  assert.equal((await call('/desk/queue',{cookie:fresh.cookie})).status,401);
  assert.equal((await call('/desk/queue',{cookie:other.cookie})).status,401);
 });
 await t.test('renaming a cookie to another role cannot authorize or revoke the original login',async()=>{
  const customer=await login('customer','lin'),staff=await login('staff','staff');
  assert.equal((await session(staff.cookie.replace('repair_staff=','repair_customer='))).status,401);
  assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,200);
  assert.equal((await call('/desk/queue',{cookie:customer.cookie.replace('repair_customer=','repair_staff=')})).status,401);
  assert.equal((await session(customer.cookie)).status,200);
 });
 await t.test('browser version is non-credential, rotates on same-account login and rejects stale staff/customer requests',async()=>{
  assert.equal((await call('/auth/customer/me')).data.identity,null);
  assert.equal((await call('/')).status,404);
  for(const role of ['customer','staff']){
   const account=role==='customer'?'lin':'staff',path=role==='customer'?'/host/me':'/desk/queue';
   const first=await login(role,account),me=await call('/auth/'+role+'/me',{cookie:first.cookie});
   const version=me.data.identity.version;assert.match(version,/^[a-f0-9]{32}$/);
   assert.equal((await call(path,{cookie:first.cookie,version})).status,200);
   assert.equal((await call(path,{version})).status,401);
   const next=await login(role,account,first.cookie),fresh=await call('/auth/'+role+'/me',{cookie:next.cookie});
   assert.notEqual(fresh.data.identity.version,version);
   assert.equal((await call(path,{cookie:next.cookie,version})).status,401);
   assert.equal((await call(path,{cookie:next.cookie,version:fresh.data.identity.version})).status,200);
   assert.equal((await call('/auth/'+role+'/me',{cookie:first.cookie})).data.identity,null);
  }
 });
 await t.test('opt-in UI serves only local assets with CSP and no injected credentials',async()=>{
  const ui=await startBusiness({gatewayOrigin,hostSecret,agents:[{id:'staff-1',name:'维修服务员工',secret:agentSecret}],accounts,serveUI:true});
  try{
   for(const path of ['/','/staff-login','/chat/','/desk/','/business.js','/business.css']){
    const response=await fetch(ui.origin+path);assert.equal(response.status,200);
    assert.match(response.headers.get('content-security-policy'),/frame-ancestors 'none'/);
    assert.equal(response.headers.get('cache-control'),'no-store');
    const body=await response.text();for(const secret of [...Object.values(passwords),hostSecret,agentSecret])assert.ok(!body.includes(secret));
   }
   assert.equal((await fetch(ui.origin+'/public/index.html')).status,404);
   assert.equal((await fetch(ui.origin+'/business.js',{headers:{Origin:'http://foreign.invalid'}})).status,403);
  }finally{await ui.close();}
 });
 // Advance the injected clock only after checks that exchange real gateway TTLs.
 await t.test('expired business login cannot renew chat or access staff queue',async()=>{
  const a=await login('customer','lin'),staff=await login('staff','staff');
  now+=1800001;
  assert.equal((await session(a.cookie)).status,401);
  assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,401);
 });
});
