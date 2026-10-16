// Two real HTTP business adapters, one shared in-memory gateway. No CRMEB
// deployment, database, .env, model provider or browser automation involved.
import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {startHost} from '../standalone-host/host.mjs';
import {startPortal} from '../office-portal/portal.mjs';
import {startBusiness} from '../fresh-onboarding/business.mjs';
import {attachSupport} from '../fresh-onboarding/support.mjs';
const root=fileURLToPath(new URL('../../',import.meta.url));
const secret=()=>randomBytes(32).toString('base64url');
function client(origin){
 return async(path,{method='GET',body,cookie,token}={})=>{
  const r=await fetch(origin+path,{method,headers:{Origin:origin,...(body===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(10000)});
  const text=await r.text();return {status:r.status,data:text?JSON.parse(text):null,cookie:r.headers.get('set-cookie')?.split(';')[0]};
 };
}
for(const mode of ['demo','password'])test(`${mode}: two systems sharing one gateway stay isolated through the complete human lifecycle`,{timeout:180000},async t=>{
 const dir=await mkdtemp(join(tmpdir(),'omni-cross-system-'));
 let child,closed;const hosts=[];
 t.after(async()=>{
  for(const host of hosts)await host.close();
  if(child&&child.exitCode===null&&child.signalCode===null){child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}
  await rm(dir,{recursive:true,force:true});
 });
 const binary=join(dir,'gateway');
 await promisify(execFile)('go',['build','-o',binary,'./integrations/cross-system/gateway'],{cwd:root,timeout:120000});
 const hostSecrets=[secret(),secret()],agentSecrets=[secret(),secret()];
 child=spawn(binary,[],{env:{FIXTURE_HOST_A:hostSecrets[0],FIXTURE_HOST_B:hostSecrets[1],FIXTURE_AGENT_A:agentSecrets[0],FIXTURE_AGENT_B:agentSecrets[1]},stdio:['ignore','pipe','pipe']});
 closed=new Promise(resolve=>child.once('close',resolve));child.stderr.resume();
 const gateway=await new Promise((resolve,reject)=>{
  const timer=setTimeout(()=>reject(Error('fixture startup timeout')),10000);let output='';
  child.once('error',()=>{clearTimeout(timer);reject(Error('fixture startup failed'));});
  child.once('exit',()=>{clearTimeout(timer);reject(Error('fixture exited'));});
  child.stdout.on('data',chunk=>{output+=chunk;const m=output.match(/^(http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});
 });
 const passwords=[{lin:secret(),zhou:secret(),staff:secret()},{lin:secret(),zhou:secret(),staff:secret()}];
 for(let i=0;i<2;i++){
  const config={gatewayOrigin:gateway,hostSecret:hostSecrets[i],agents:[{id:'staff-1',name:'测试客服',secret:agentSecrets[i]}]};
  const accounts=[{realm:'member',handle:'lin',number:'MEM:731',passphrase:passwords[i].lin},{realm:'member',handle:'zhou',number:'MEM:864',passphrase:passwords[i].zhou},{realm:'operator',handle:'staff',number:'OPS:42',passphrase:passwords[i].staff}];
  if(mode==='demo')hosts.push(await startHost(config));
  else if(i===0)hosts.push(await startPortal({...config,accounts}));
  else{const business=await startBusiness({accounts});let support;hosts.push({origin:business.origin,async close(){await business.close();await support?.close();}});support=await attachSupport(business,config);}
 }
 // Only adapt native login routes/bodies; all support requests and responses stay real.
 const call=hosts.map((h,i)=>{const request=client(h.origin);return (path,options={})=>{
  if(mode==='demo')return request(path,options);
  const routes=i===0?{'/demo/login':'/access/member/open','/demo/logout':'/access/member/close','/desk/login':'/access/operator/open','/desk/logout':'/access/operator/close'}:{'/demo/login':'/auth/customer/login','/demo/logout':'/auth/customer/logout','/desk/login':'/auth/staff/login','/desk/logout':'/auth/staff/logout'};
  if(path==='/demo/login'||path==='/desk/login'){const account=path==='/desk/login'?'staff':options.body.account;options={...options,body:i===0?{handle:account,passphrase:passwords[i][account]}:{account,password:passwords[i][account]}};}
  return request(routes[path]||path,options);
 }}),direct=client(gateway),logins=[],staff=[],sessions=[];
 const open=i=>call[i]('/host/chat-session',{method:'POST',body:{},cookie:logins[i].cookie});
 const customer=(i,path,body)=>call[i](path,{method:body===undefined?'GET':'POST',body,cookie:logins[i].cookie,token:sessions[i].access_token});
 const action=(i,body)=>call[i]('/desk/action',{method:'POST',cookie:staff[i].cookie,body:{expected_agent_id:'staff-1',...body}});
 for(let i=0;i<2;i++){
  logins.push(await call[i]('/demo/login',{method:'POST',body:{account:'lin'}}));assert.equal(logins[i].status,200);
  staff.push(await call[i]('/desk/login',{method:'POST',body:{account:'staff-1'}}));assert.equal(staff[i].status,200);
  const opened=await open(i);assert.equal(opened.status,200);sessions.push(opened.data);
 }
 await t.test('same raw identities map to different conversations; secrets stay server side',async()=>{
  if(mode==='demo'){assert.equal(logins[0].data.user.id,logins[1].data.user.id);assert.equal(staff[0].data.agent.id,staff[1].data.agent.id);}
  else{
   const members=await Promise.all(call.map((request,i)=>request('/host/me',{cookie:logins[i].cookie})));
   const agents=await Promise.all(call.map((request,i)=>request('/desk/me',{cookie:staff[i].cookie})));
   for(const m of members)assert.equal(m.status,200);
   // Raw MEM:731 is identical; the two existing business mappings deliberately use different prefixes.
   assert.equal(members[0].data.user.id.replace(/^member_/,''),members[1].data.user.id.replace(/^person_/,''));
   for(const a of agents){assert.equal(a.status,200);assert.equal(a.data.agent.id,'staff-1');}
  }
  assert.notEqual(sessions[0].conversation_id,sessions[1].conversation_id);
  const browserData=JSON.stringify([logins,staff,sessions]);
  for(const value of [...hostSecrets,...agentSecrets,...passwords.flatMap(Object.values)])assert.ok(!browserData.includes(value));
 });
 await t.test('foreign cookies, mixed capabilities and forged source fields are rejected',async()=>{
  assert.equal((await call[1]('/chat/v1/messages',{cookie:logins[0].cookie,token:sessions[0].access_token})).status,401);
  assert.equal((await call[1]('/chat/v1/messages',{cookie:logins[1].cookie,token:sessions[0].access_token})).status,401);
  assert.equal((await call[1]('/desk/queue',{cookie:staff[0].cookie})).status,401);
  assert.equal((await call[1]('/host/chat-session',{method:'POST',cookie:logins[1].cookie,body:{source_id:'commerce-fixture',customer_id:'member-1001'}})).status,400);
  assert.equal((await call[1]('/internal/chat/sessions',{method:'POST',body:{}})).status,404);
 });
 const waiting=[];
 await t.test('same event ID stores distinct system messages and independent handoffs',async()=>{
  for(let i=0;i<2;i++){
   assert.equal((await customer(i,'/chat/v1/messages',{event_id:'same-event',content:'system-'+i})).status,200);
   const handoff=await customer(i,'/chat/v1/handoff',{});assert.equal(handoff.status,200);assert.equal(handoff.data.status,'waiting_human');waiting.push(handoff.data);
   const history=await customer(i,'/chat/v1/messages');assert.equal(history.status,200);
   assert.equal(history.data.messages.filter(m=>m.content==='system-'+i).length,1);
   assert.ok(!history.data.messages.some(m=>m.content==='system-'+(1-i)));
  }
 });
 await t.test('queues are source-local and all foreign staff actions are denied',async()=>{
  for(let i=0;i<2;i++){
   const queue=await call[i]('/desk/queue',{cookie:staff[i].cookie});assert.equal(queue.status,200);
   assert.deepEqual(queue.data.items.map(x=>x.conversation_id),[sessions[i].conversation_id]);
   for(const kind of ['history','claim','reply','read','close']){
    const body={conversation_id:sessions[1-i].conversation_id,action:kind};
    if(['claim','close'].includes(kind))body.revision=waiting[1-i].revision;
    if(kind==='reply')Object.assign(body,{event_id:'forged',content:'must not be stored'});
    if(kind==='read')body.message_ids=['foreign'];
    const response=await action(i,body);assert.equal(response.status,403,kind);
    assert.ok(!JSON.stringify(response.data).includes('system-'+(1-i)));
   }
  }
 });
 await t.test('own claim, reply, exact replay and read work without affecting the other source',async()=>{
  for(let i=0;i<2;i++){
   const id=sessions[i].conversation_id;
   const claim=await action(i,{conversation_id:id,action:'claim',revision:waiting[i].revision});assert.equal(claim.status,200);
   assert.equal(claim.data.status,'human_active');
   const body={conversation_id:id,action:'reply',event_id:'same-staff-event',content:'staff-reply-'+i};
   assert.equal((await action(i,body)).status,200);assert.equal((await action(i,body)).status,200);
   const history=await action(i,{conversation_id:id,action:'history'});assert.equal(history.status,200);
   const ids=history.data.messages.filter(x=>x.role==='customer').map(x=>x.id);
   assert.equal((await action(i,{conversation_id:id,action:'read',message_ids:ids})).status,200);
   const received=await customer(i,'/chat/v1/messages');
   assert.equal(received.data.messages.filter(m=>m.content==='staff-reply-'+i).length,1);
   assert.ok(!received.data.messages.some(m=>m.content==='staff-reply-'+(1-i)||m.content==='must not be stored'));
  }
 });
 await t.test('close/new round preserve old history and do not advance the other source',async()=>{
  const id=sessions[0].conversation_id;
  const history=await action(0,{conversation_id:id,action:'history'});
  assert.equal((await action(0,{conversation_id:id,action:'close',revision:history.data.revision})).status,200);
  assert.equal((await customer(0,'/chat/v1/messages',{event_id:'closed',content:'denied'})).status,409);
  const next=await customer(0,'/chat/v1/consultations',{});assert.equal(next.status,200);
  assert.notEqual(next.data.conversation_id,id);
  assert.equal((await customer(0,'/chat/v1/messages')).status,401);
  const opened=await open(0);assert.equal(opened.data.conversation_id,next.data.conversation_id);sessions[0]=opened.data;
  assert.deepEqual((await customer(0,'/chat/v1/messages')).data.messages,[]);
  const prior=await action(0,{conversation_id:id,action:'history'});assert.equal(prior.data.status,'closed');
  assert.ok(prior.data.messages.some(m=>m.content==='staff-reply-0'));
  const other=await customer(1,'/chat/v1/messages');assert.equal(other.data.status,'human_active');
  assert.equal((await open(1)).data.conversation_id,sessions[1].conversation_id);
 });
 await t.test('switching A revokes its old login without changing B history or ownership',async()=>{
  const before=await action(1,{conversation_id:sessions[1].conversation_id,action:'history'});assert.equal(before.status,200);
  const old=logins[0];logins[0]=await call[0]('/demo/login',{method:'POST',body:{account:'zhou'},cookie:old.cookie});assert.equal(logins[0].status,200);
  assert.equal((await call[0]('/chat/v1/messages',{cookie:old.cookie,token:sessions[0].access_token})).status,401);
  assert.equal((await customer(0,'/chat/v1/messages')).status,401);
  const next=await open(0);assert.equal(next.status,200);assert.notEqual(next.data.conversation_id,sessions[0].conversation_id);sessions[0]=next.data;
  assert.deepEqual((await customer(0,'/chat/v1/messages')).data.messages,[]);
  const after=await action(1,{conversation_id:sessions[1].conversation_id,action:'history'});assert.equal(after.status,200);assert.deepEqual(after.data,before.data);
  assert.equal((await customer(1,'/chat/v1/messages')).data.status,'human_active');
 });
 await t.test('customer/staff logout in A neither authenticates nor logs out B',async()=>{
  assert.equal((await call[0]('/demo/logout',{method:'POST',body:{},cookie:logins[0].cookie})).status,204);
  assert.equal((await call[0]('/desk/logout',{method:'POST',body:{},cookie:staff[0].cookie})).status,204);
  assert.equal((await customer(0,'/chat/v1/messages')).status,401);
  assert.equal((await call[0]('/desk/queue',{cookie:staff[0].cookie})).status,401);
  assert.equal((await customer(1,'/chat/v1/messages')).status,200);
  assert.equal((await call[1]('/desk/queue',{cookie:staff[1].cookie})).status,200);
 });
 await t.test('private gateway does not accept customer or host credentials as staff',async()=>{
  for(const token of [sessions[1].access_token,hostSecrets[1]])assert.equal((await direct('/internal/chat/desk/queue',{token})).status,401);
 });
 if(mode==='demo')await t.test('reusable acceptance checks the non-commerce sample through configuration only',async()=>{
  const id=sessions[1].conversation_id;
  const state=await action(1,{conversation_id:id,action:'history'});
  assert.equal((await action(1,{conversation_id:id,action:'close',revision:state.data.revision})).status,200);
  assert.equal((await customer(1,'/chat/v1/consultations',{})).status,200);
  const probe=spawn(process.execPath,['integrations/host-kit/verify.mjs','--allow-test-writes'],{cwd:root,stdio:['pipe','pipe','pipe'],timeout:20000});
  let output='';probe.stdout.on('data',chunk=>{output+=chunk;});probe.stderr.resume();
  const finished=new Promise((resolve,reject)=>{probe.once('error',reject);probe.once('close',resolve);});
  probe.stdin.end(JSON.stringify({origin:hosts[1].origin,customers:[{account:'lin'},{account:'zhou'}],staff:{account:'staff-1'}}));
  const exitCode=await finished,report=JSON.parse(output);
  assert.equal(exitCode,0,JSON.stringify(report));
  assert.equal(report.passed,true,JSON.stringify(report));
  assert.ok(report.checks.includes('new_round_empty'));
  assert.ok(report.checks.includes('staff_logout'));
 });
});
