import test from 'node:test';
import assert from 'node:assert/strict';
import {createLegacyDirectory} from './legacy.mjs';
import {createFixture} from '../fresh-onboarding/fixture.mjs';

test('cookie namespace rejects unsafe configuration before initialization',async()=>{
 for(const cookieNamespace of ['',null,7,'Office','a-b','a; Path=/','a\r\nX: y','a'.repeat(49)])await assert.rejects(createLegacyDirectory({accounts:[],cookieNamespace}),/invalid cookie namespace/);
});
test('custom namespaces retain role separation and reject duplicate or renamed foreign tickets',async()=>{
 const accounts=[{realm:'member',handle:'lin',number:'MEM:1',passphrase:'temporary-password-123'}];
 const a=await createLegacyDirectory({accounts,cookieNamespace:'tenant_a'}),b=await createLegacyDirectory({accounts,cookieNamespace:'tenant_b'});
 try{
  const set=await a.open({headers:{}},'member','lin',accounts[0].passphrase),cookie=set.split(';')[0];
  assert.match(set,/^tenant_a_member=/);assert.match(set,/; Path=\/; HttpOnly; SameSite=Strict; Max-Age=1800$/);
  const request={headers:{cookie}};assert.ok(a.authenticate(request,'member'));assert.equal(a.authenticate(request,'operator'),null);
  assert.equal(a.authenticate({headers:{cookie:cookie+'; '+cookie}},'member'),null);
  assert.equal(b.authenticate({headers:{cookie:cookie.replace('tenant_a_member','tenant_b_member')}},'member'),null);
  b.close(request,'member');assert.ok(a.authenticate(request,'member'));
  assert.match(a.close(request,'member'),/^tenant_a_member=;.*Max-Age=0$/);assert.equal(a.authenticate(request,'member'),null);
 }finally{a.dispose();b.dispose();}
});
test('two HTTP hosts share a browser-style cookie jar without login, switch, logout or history collision',async()=>{
 const fixtures=[],jar=new Map();
 const call=async(f,path,body,token)=>{
  const r=await fetch(f.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:f.origin,Cookie:[...jar].map(([k,v])=>k+'='+v).join('; '),...(body===undefined?{}:{'Content-Type':'application/json'}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(15000)});
  for(const set of r.headers.getSetCookie()){const pair=set.split(';')[0],at=pair.indexOf('=');if(/Max-Age=0(?:;|$)/.test(set))jar.delete(pair.slice(0,at));else jar.set(pair.slice(0,at),pair.slice(at+1));}
  const raw=await r.text();return {status:r.status,data:raw?JSON.parse(raw):null};
 };
 try{
  for(const system of ['office','fresh']){const f=await createFixture({system});fixtures.push(f);await f.attach();}
  const [office,fresh]=fixtures;
  const login=async(f,handle)=>{const a=f.accounts.find(a=>a.handle===handle),staff=handle==='staff';return f===office?call(f,'/access/'+(staff?'operator':'member')+'/open',{handle,passphrase:a.passphrase}):call(f,'/auth/'+(staff?'staff':'customer')+'/login',{account:handle,password:a.passphrase});};
  const caps=[];
  for(const [i,f] of fixtures.entries()){
   assert.equal((await login(f,'lin')).status,200);assert.equal((await login(f,'staff')).status,200);
   const cap=await call(f,'/host/chat-session',{});assert.equal(cap.status,200);caps.push(cap.data.access_token);
   assert.equal((await call(f,'/chat/v1/messages',{event_id:'namespace-'+i,content:'only-system-'+i},caps[i])).status,200);
  }
  assert.deepEqual([...jar.keys()].sort(),['fresh_member','fresh_operator','office_member','office_operator']);
  for(const [i,f] of fixtures.entries()){
   const history=await call(f,'/chat/v1/messages',undefined,caps[i]);assert.equal(history.status,200);
   assert.ok(history.data.messages.some(m=>m.content==='only-system-'+i));assert.ok(!history.data.messages.some(m=>m.content==='only-system-'+(1-i)));
   assert.equal((await call(f,'/desk/queue')).status,200);
   assert.equal((await call(f,'/chat/v1/messages',undefined,caps[1-i])).status,401);
  }
  assert.equal((await login(office,'zhou')).status,200);
  assert.equal((await call(office,'/chat/v1/messages',undefined,caps[0])).status,401);
  assert.equal((await call(fresh,'/chat/v1/messages',undefined,caps[1])).status,200);
  assert.equal((await call(office,'/access/member/close',{})).status,204);
  assert.equal((await call(office,'/access/operator/close',{})).status,204);
  assert.equal((await call(office,'/desk/queue')).status,401);
  assert.equal((await call(fresh,'/desk/queue')).status,200);
  assert.equal((await call(fresh,'/chat/v1/messages',undefined,caps[1])).status,200);
  await call(fresh,'/auth/customer/logout',{});await call(fresh,'/auth/staff/logout',{});assert.equal(jar.size,0);
 }finally{for(const f of fixtures.reverse())await f.close();}
});
