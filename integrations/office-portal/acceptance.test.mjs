import test from 'node:test';
import assert from 'node:assert/strict';
import {randomBytes} from 'node:crypto';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
import http from 'node:http';
import {startPortal} from './portal.mjs';
import {createLegacyDirectory} from './legacy.mjs';
import {acceptHost} from '../host-kit/acceptance.mjs';
async function verifyHost({origin,...lifecycle},options){
 const report=await acceptHost({preflight:{origin,paths:{customerIdentity:'/support/identity/member',staffIdentity:'/support/identity/operator'}},lifecycle},options);
 assert.equal(report.status,'needs_verification',JSON.stringify(report));
 assert.ok(!report.phases.preflight.checks.some(c=>c.status==='fail'),JSON.stringify(report));
 assert.equal(report.checks.find(c=>c.id==='lifecycle').status,'pass',JSON.stringify(report));
 return report.phases.lifecycle;
}
import {preflightHost} from '../host-kit/preflight.mjs';
const random=()=>randomBytes(32).toString('base64url');
test('office native records map through an independent facade',{timeout:180000},async t=>{
 const dir=await mkdtemp(join(tmpdir(),'omni-office-test-'));let gateway,portal,closed;
 t.after(async()=>{await portal?.close();if(gateway&&gateway.exitCode===null&&gateway.signalCode===null){gateway.kill('SIGTERM');const timer=setTimeout(()=>gateway.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}await rm(dir,{recursive:true,force:true});});
 const root=fileURLToPath(new URL('../../',import.meta.url)),binary=join(dir,'gateway');
 await promisify(execFile)('go',['build','-o',binary,'./integrations/cross-system/gateway'],{cwd:root,timeout:120000});
 const hostSecret=random(),agentSecret=random();
 gateway=spawn(binary,[],{env:{FIXTURE_HOST_A:random(),FIXTURE_AGENT_A:random(),FIXTURE_HOST_B:hostSecret,FIXTURE_AGENT_B:agentSecret},stdio:['ignore','pipe','ignore']});closed=new Promise(r=>gateway.once('close',r));
 const gatewayOrigin=await new Promise((resolve,reject)=>{let out='';const timer=setTimeout(()=>reject(Error('startup timeout')),10000);const fail=()=>{clearTimeout(timer);reject(Error('gateway failed'));};gateway.once('error',fail);gateway.once('exit',fail);gateway.stdout.on('data',c=>{out+=c;const m=out.match(/^(http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});});
 const passwords={lin:random(),zhou:random(),staff:random(),visitor:random()};
 const accounts=[{realm:'member',handle:'lin',number:'MEM:731',passphrase:passwords.lin},{realm:'member',handle:'zhou',number:'MEM:864',passphrase:passwords.zhou},{realm:'operator',handle:'staff',number:'OPS:42',passphrase:passwords.staff},{realm:'operator',handle:'visitor',number:'OPS:99',passphrase:passwords.visitor}];
 let now=Date.now();portal=await startPortal({gatewayOrigin,hostSecret,agents:[{id:'staff-1',name:'办公服务专员',secret:agentSecret}],accounts,serveUI:true,now:()=>now});
 const call=async(path,{body,cookie,version,token,origin=portal.origin}={})=>{
  const response=await fetch(portal.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:origin,...(body===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(version===undefined?{}:{'X-Business-Session':version}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'manual',signal:AbortSignal.timeout(15000)});
  const raw=await response.text();return {status:response.status,data:raw?JSON.parse(raw):null,cookie:response.headers.get('set-cookie')?.split(';')[0]};
 };
 const login=(realm,handle,cookie)=>call(`/access/${realm}/open`,{body:{handle,passphrase:passwords[handle]},cookie});
 await t.test('read-only preflight checks live business identities without login, logout or conversation writes',async()=>{
  // Test harness prepares sessions; preflight itself is never given passwords.
  const oldCustomer=await login('member','lin'),customer=await login('member','lin',oldCustomer.cookie);
  const oldStaff=await login('operator','staff'),staff=await login('operator','staff',oldStaff.cookie),visitor=await login('operator','visitor');
  const memberID=(await call('/support/identity/member',{cookie:customer.cookie})).data.identity.id;
  const visitorID=(await call('/support/identity/operator',{cookie:visitor.cookie})).data.identity.id;
  const before=await call('/desk/queue',{cookie:staff.cookie}),requests=[];
  const report=await preflightHost({origin:portal.origin,paths:{customerIdentity:'/support/identity/member',staffIdentity:'/support/identity/operator'},sessions:{customer:{cookie:customer.cookie,expectedID:memberID},staff:{cookie:staff.cookie,expectedID:'staff-1'},revokedCustomer:{cookie:oldCustomer.cookie,expectedID:memberID},revokedStaff:{cookie:oldStaff.cookie,expectedID:'staff-1'},unassignedStaff:{cookie:visitor.cookie,expectedID:visitorID}}},{fetcher:(url,options)=>{requests.push({path:new URL(url).pathname,method:options.method,body:options.body});return fetch(url,options);}});
  assert.equal(report.status,'needs_verification',JSON.stringify(report));assert.ok(!report.checks.some(c=>c.status==='fail'),JSON.stringify(report));
  assert.ok(requests.every(r=>r.method==='GET'&&r.body===undefined&&['/support/identity/member','/support/identity/operator','/host/me','/desk/me','/desk/queue'].includes(r.path)));
  assert.deepEqual((await call('/desk/queue',{cookie:staff.cookie})).data,before.data);
  assert.equal((await call('/support/identity/member',{cookie:customer.cookie})).data.identity.id,memberID);
  for(const value of [...Object.values(passwords),hostSecret,agentSecret,customer.cookie.split('=')[1],staff.cookie.split('=')[1]])assert.ok(!JSON.stringify(report).includes(value));
 });
 await t.test('native IAM uses nested records; mapping does not trust browser claims',async()=>{
  const directory=await createLegacyDirectory({accounts});try{const cookie=(await directory.open({headers:{}},'member','lin',passwords.lin)).split(';')[0];const record=directory.authenticate({headers:{cookie}},'member');assert.equal(record.principal.number,'MEM:731');assert.equal(record.lifecycle.active,true);assert.ok(record.ticket.deadline>Date.now());assert.notEqual(record.ticket.key,record.ticket.publicRevision);}finally{directory.dispose();}
  assert.equal((await call('/access/member/open',{body:{account:'lin',password:passwords.lin}})).status,400);
  assert.equal((await call('/access/member/open',{body:{handle:'lin',passphrase:passwords.lin,subjectID:'staff-1'}})).status,400);
  assert.equal((await login('operator','lin')).status,401);
  assert.equal((await call('/access/member/open',{body:{handle:'lin',passphrase:passwords.lin},origin:'http://foreign.invalid'})).status,403);
  assert.equal((await call('/demo/login',{body:{account:'lin'}})).status,404);
  assert.equal((await call('/internal/chat/sessions',{body:{}})).status,404);
 });
 await t.test('existing verifier completes full lifecycle with only routes and native login body configured',async()=>{
  const report=await verifyHost({origin:portal.origin,customers:[{handle:'lin',passphrase:passwords.lin},{handle:'zhou',passphrase:passwords.zhou}],staff:{handle:'staff',passphrase:passwords.staff},paths:{customerLogin:'/access/member/open',customerLogout:'/access/member/close',staffLogin:'/access/operator/open',staffLogout:'/access/operator/close'}},{allowTestWrites:true});
  assert.equal(report.passed,true,JSON.stringify(report));for(const key of ['claim','reply','close','next_round','customer_logout','staff_logout'])assert.ok(report.checks.includes(key));
  for(const secret of [...Object.values(passwords),hostSecret,agentSecret])assert.ok(!JSON.stringify(report).includes(secret));
 });
 await t.test('public identity maps native number and exposes no authentication ticket',async()=>{
  const a=await login('member','lin'),me=await call('/support/identity/member',{cookie:a.cookie});
  assert.match(me.data.identity.id,/^member_[a-f0-9]{64}$/);assert.equal(me.data.identity.role,'customer');assert.match(me.data.identity.version,/^[A-Za-z0-9_-]{32}$/);assert.deepEqual(Object.keys(me.data.identity).sort(),['id','role','version']);
  assert.equal((await call('/desk/queue',{cookie:a.cookie})).status,401);
  assert.equal((await call('/support/identity/operator',{cookie:a.cookie})).data.identity,null);
  const fresh=await login('member','lin',a.cookie);assert.equal((await call('/support/identity/member',{cookie:fresh.cookie})).data.identity.id,me.data.identity.id);
 });
 await t.test('new cookie rejects stale or duplicate browser versions, including same-account relogin',async()=>{
  for(const [realm,handle,path] of [['member','lin','/host/chat-session'],['operator','staff','/desk/queue']]){
   const a=await login(realm,handle),old=(await call('/support/identity/'+realm,{cookie:a.cookie})).data.identity.version;
   const b=await login(realm,handle,a.cookie),next=(await call('/support/identity/'+realm,{cookie:b.cookie})).data.identity.version;
   assert.notEqual(old,next);const body=realm==='member'?{}:undefined;
   assert.equal((await call(path,{body,cookie:b.cookie,version:old})).status,401);
   assert.equal((await call(path,{body,cookie:b.cookie,version:''})).status,401);
   assert.equal((await call(path,{body,cookie:b.cookie,version:next})).status,200);
   assert.equal((await call(path,{body,cookie:a.cookie})).status,401);
   const status=await new Promise((resolve,reject)=>{const req=http.request(portal.origin+(realm==='member'?'/host/me':'/desk/queue'),{headers:{Cookie:b.cookie,'X-Business-Session':[next,next]}},res=>{res.resume();resolve(res.statusCode);});req.on('error',reject);req.end();});assert.equal(status,401);
  }
 });
 await t.test('switch, disable and re-enable revoke old sessions without crossing roles',async()=>{
  const a=await login('member','lin'),cap=await call('/host/chat-session',{cookie:a.cookie,body:{}});const b=await login('member','zhou',a.cookie);
  assert.equal((await call('/chat/v1/messages',{cookie:b.cookie,token:cap.data.access_token})).status,401);
  for(const cookie of [a.cookie,b.cookie])assert.equal((await call('/chat/v1/messages',{cookie,token:cap.data.access_token,body:{event_id:random(),content:'must reject stale login'}})).status,401);
  const currentCap=await call('/host/chat-session',{cookie:b.cookie,body:{}});assert.equal(currentCap.status,200);
  assert.equal((await call('/chat/v1/messages',{cookie:b.cookie,token:currentCap.data.access_token})).status,200);
  const staff=await login('operator','staff');portal.setActive('member','zhou',false);
  assert.equal((await call('/chat/v1/messages',{cookie:b.cookie,token:currentCap.data.access_token,body:{event_id:random(),content:'must reject disabled login'}})).status,401);
  assert.equal((await call('/host/chat-session',{cookie:b.cookie,body:{}})).status,401);assert.equal((await login('member','zhou')).status,401);
  portal.setActive('member','zhou',true);assert.equal((await call('/host/chat-session',{cookie:b.cookie,body:{}})).status,401);
  assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,200);
  portal.setActive('operator','staff',false);assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,401);portal.setActive('operator','staff',true);assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,401);
  assert.equal((await call('/desk/action',{cookie:staff.cookie,body:{action:'claim',expected_agent_id:'staff-1',conversation_id:'forged'}})).status,401);
 });
 await t.test('unmapped operator can log in but cannot act as support staff; logout is idempotent',async()=>{
  const a=await login('operator','visitor');assert.equal(a.status,200);assert.equal((await call('/desk/queue',{cookie:a.cookie})).status,401);
  assert.equal((await call('/desk/action',{cookie:a.cookie,body:{action:'claim',expected_agent_id:'staff-1',conversation_id:'forged'}})).status,401);
  for(let i=0;i<2;i++)assert.equal((await call('/access/operator/close',{cookie:a.cookie,body:{}})).status,204);
 });
 await t.test('new page paths and root landing bridge work, private/static traversal paths do not',async()=>{
  for(const path of ['/access/member','/access/operator','/support/chat','/support/desk','/portal.js','/portal.css']){const r=await fetch(portal.origin+path);assert.equal(r.status,200);assert.equal(r.headers.get('cache-control'),'no-store');assert.match(r.headers.get('content-security-policy'),/frame-ancestors 'none'/);const text=await r.text();for(const secret of [...Object.values(passwords),hostSecret,agentSecret])assert.ok(!text.includes(secret));}
  const root=await fetch(portal.origin+'/',{redirect:'manual'});assert.equal(root.status,302);assert.equal(root.headers.get('location'),'/access/member');
  assert.equal((await call('/public/index.html')).status,404);
 });
 await t.test('native ticket expiry prevents refresh or staff use',async()=>{
  const a=await login('member','lin'),staff=await login('operator','staff');now+=1800001;
  assert.equal((await call('/host/chat-session',{cookie:a.cookie,body:{}})).status,401);assert.equal((await call('/desk/queue',{cookie:staff.cookie})).status,401);
 });
});
