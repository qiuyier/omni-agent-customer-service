import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {createSupportProxy} from './proxy.mjs';
const principal=()=>({role:'customer',enabled:true,subjectID:'person_a',sessionID:'opaque-session-001',expiresAt:Date.now()+60000,version:'public-version-001'});
async function fixture(t,{timeoutMs=1000}={}){
 let user=principal(),calls=0,mode='normal',hook=()=>{},received;
 const listen=s=>new Promise(r=>s.listen(0,'127.0.0.1',r));
 const upstream=http.createServer(async(req,res)=>{
  calls++;const parts=[];for await(const c of req)parts.push(c);received={headers:req.headers,body:Buffer.concat(parts).toString(),path:req.url};hook();
  if(mode==='timeout')return;
  if(mode==='redirect'){res.writeHead(302,{Location:'/unexpected'});res.end();return;}
  res.setHeader('Content-Type',mode==='type'?'text/plain':'application/json');
  res.end(mode==='large'?'x'.repeat(1048577):mode==='json'?'not json':JSON.stringify({messages:['private history']}));
 });await listen(upstream);const adapterOrigin='http://127.0.0.1:'+upstream.address().port;
 let origin;const handler=createSupportProxy({adapterOrigin,publicOrigin:()=>origin,sessions:{authenticate:async()=>{if(mode==='auth-error')throw Error('SECRET');return user;}},timeoutMs});
 const server=http.createServer(async(req,res)=>{if(!await handler(req,res)){res.writeHead(404);res.end();}});await listen(server);origin='http://127.0.0.1:'+server.address().port;
 t.after(async()=>{for(const s of [server,upstream]){s.closeAllConnections();await new Promise(r=>s.close(r));}});
 const call=async(path='/chat/v1/messages',{method='GET',headers={},body}={})=>{
  const r=await fetch(origin+path,{method,headers:{Origin:origin,...headers},body,redirect:'manual'});const raw=await r.text();return {status:r.status,raw,headers:r.headers};
 };
 return {call,origin,adapterOrigin,get calls(){return calls;},get received(){return received;},get user(){return user;},set user(v){user=v;},set mode(v){mode=v;},set hook(v){hook=v;}};
}
test('proxy rejects unsafe upstreams and invalid configuration before listening',()=>{
 for(const adapterOrigin of ['https://example.com','http://127.0.0.1:1234/','http://u:p@127.0.0.1:1234','http://localhost:1234'])assert.throws(()=>createSupportProxy({adapterOrigin,publicOrigin:()=>'',sessions:{authenticate(){}}}));
 assert.throws(()=>createSupportProxy({adapterOrigin:'http://127.0.0.1:1234',publicOrigin:()=>'',sessions:{authenticate(){}},timeoutMs:0}));
});
test('proxy only forwards exact routes and permitted headers after external origin checks',async t=>{
 const f=await fixture(t);
 for(const path of ['/internal/chat/sessions','/demo/login','/chat/v1/messages?extra=1','/chat/v1/messages/'])assert.equal((await f.call(path)).status,404);
 assert.equal((await f.call('/host/me',{headers:{Origin:'http://foreign.invalid'}})).status,403);
 // fetch normalizes Host; use raw HTTP so the forged value reaches the boundary.
 const foreignHost=await new Promise((resolve,reject)=>{const req=http.request(f.origin+'/host/me',{headers:{Host:'foreign.invalid'}},res=>{res.resume();resolve(res.statusCode);});req.on('error',reject);req.end();});assert.equal(foreignHost,403);
 assert.equal((await f.call('/host/me',{headers:{'Sec-Fetch-Site':'cross-site'}})).status,403);
 assert.equal((await f.call('/chat/v1/messages',{method:'DELETE'})).status,405);
 assert.equal((await f.call('/chat/v1/messages',{method:'POST',headers:{Origin:''}})).status,403);
 assert.equal((await f.call('/chat/v1/messages',{method:'POST',body:'{}'})).status,415);
 assert.equal(f.calls,0);
 const r=await f.call('/chat/v1/messages',{method:'POST',headers:{Cookie:'business=test',Authorization:'Bearer test','Content-Type':'application/json','X-Business-Session':f.user.version,'X-Forwarded-Host':'attacker'},body:'{}'});
 assert.equal(r.status,200);assert.equal(r.headers.get('cache-control'),'no-store');assert.equal(f.received.headers.origin,f.adapterOrigin);assert.equal(f.received.headers.cookie,'business=test');assert.equal(f.received.headers.authorization,'Bearer test');assert.equal(f.received.headers['x-forwarded-host'],undefined);assert.equal(f.received.headers['x-business-session'],undefined);assert.equal(f.received.body,'{}');
});
test('wrong, empty, duplicate versions and duplicate authorization never reach upstream',async t=>{
 const f=await fixture(t);
 for(const version of ['',f.user.version+'old'])assert.equal((await f.call('/host/me',{headers:{'X-Business-Session':version}})).status,401);
 for(const [header,value,expected] of [['X-Business-Session',f.user.version,401],['Authorization','Bearer test',400]]){
  const status=await new Promise((resolve,reject)=>{const req=http.request(f.origin+'/host/me',{headers:{[header]:[value,value]}},res=>{res.resume();resolve(res.statusCode);});req.on('error',reject);req.end();});assert.equal(status,expected);
 }
 f.user={...f.user,expiresAt:0};assert.equal((await f.call('/host/me',{headers:{'X-Business-Session':'public-version-001'}})).status,401);assert.equal(f.calls,0);
});
test('revocation, relogin, version mutation and account changes discard late private response',async t=>{
 const f=await fixture(t);
 for(const mutate of [()=>{f.user=null;},()=>{f.user.sessionID='new-session-002';},()=>{f.user.subjectID='person_b';},()=>{f.user.version='public-version-002';},()=>{f.user.enabled=false;}]){
  f.user=principal();f.hook=mutate;
  const r=await f.call();assert.equal(r.status,401);assert.ok(!r.raw.includes('private history'));
 }
});
test('body and response limits, redirect, malformed JSON and authentication failures are sanitized',async t=>{
 const f=await fixture(t);
 const big=await f.call('/chat/v1/messages',{method:'POST',headers:{'Content-Type':'application/json'},body:'x'.repeat(32769)});assert.equal(big.status,413);assert.equal(f.calls,0);
 for(const mode of ['large','redirect','type','json','auth-error']){f.mode=mode;const r=await f.call();assert.equal(r.status,mode==='auth-error'?503:502);assert.ok(!r.raw.includes('SECRET'));assert.ok(!r.raw.includes('private history'));}
});
test('upstream timeout is bounded and reports only a fixed error',async t=>{
 const f=await fixture(t,{timeoutMs:30});f.mode='timeout';const r=await f.call();assert.equal(r.status,502);assert.deepEqual(JSON.parse(r.raw),{code:'support_unavailable'});
});
