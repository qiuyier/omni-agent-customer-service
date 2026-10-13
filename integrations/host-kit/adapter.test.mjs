import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {startAdapter} from './adapter.mjs';
import {verifyHost} from './verify.mjs';

test('expired, malformed and unconfigured identities fail closed without upstream access',async t=>{
 let customer=null;
 const base={id:'customer-1',sessionID:'session-handle-001',expiresAt:Date.now()+60000};
 const adapter=await startAdapter({gatewayOrigin:'http://127.0.0.1:1',hostSecret:'h'.repeat(43),agents:[{id:'allowed',name:'员工',secret:'a'.repeat(43)}],identity:{customer:()=>customer,staff:()=>({...base,id:'unconfigured'})}});
 t.after(()=>adapter.close());
 for(const value of [null,{...base,id:''},{...base,sessionID:'short'},{...base,expiresAt:0},{...base,id:'forged/id'}]){
  customer=value;
  const r=await fetch(adapter.origin+'/host/chat-session',{method:'POST',headers:{Origin:adapter.origin,'Content-Type':'application/json'},body:'{}'});
  assert.equal(r.status,401);
 }
 assert.equal((await fetch(adapter.origin+'/desk/queue')).status,401);
 assert.equal((await fetch(adapter.origin+'/')).status,404);
});
test('verification refuses writes or non-loopback targets before networking',async()=>{
 assert.equal((await verifyHost({})).error,'test_write_consent_required');
 for(const origin of ['https://example.com','http://127.0.0.1:80/path','http://localhost:1234','http://u:p@127.0.0.1:12'])assert.equal((await verifyHost({origin},{allowTestWrites:true})).error,'loopback_origin_required');
});
test('business identity is mandatory, realm-specific, revocable and never chosen by login body',async t=>{
 const secret='s'.repeat(43),cap='chat_'+'a'.repeat(43);let active=true,staffActive=true,hook=null,calls=0;
 const upstream=http.createServer(async(req,res)=>{for await(const c of req){}calls++;hook?.();res.setHeader('Content-Type','application/json');res.end(JSON.stringify(req.url==='/internal/chat/sessions'?{access_token:cap,conversation_id:'wbc_'+'a'.repeat(64),expires_at:new Date(Date.now()+300000).toISOString()}:{messages:[{content:'private'}],items:[]}));});
 await new Promise(r=>upstream.listen(0,'127.0.0.1',r));t.after(()=>{upstream.closeAllConnections();return new Promise(r=>upstream.close(r));});
 const config={gatewayOrigin:'http://127.0.0.1:'+upstream.address().port,hostSecret:secret,agents:[{id:'employee-7',name:'测试员工',secret:'e'.repeat(43)}]};
 await assert.rejects(startAdapter(config),/identity provider required/);
 const principal={id:'person-3',sessionID:'opaque-session-0001',expiresAt:Date.now()+300000};
 const adapter=await startAdapter({...config,identity:{customer:async req=>active&&req.headers.cookie==='business=customer'?principal:null,staff:async req=>staffActive&&req.headers.cookie==='business=staff'?{...principal,id:'employee-7'}:null}});
 t.after(()=>adapter.close());
 const call=async(path,{cookie='business=customer',body,token}={})=>{const r=await fetch(adapter.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:adapter.origin,Cookie:cookie,...(body===undefined?{}:{'Content-Type':'application/json'}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body)});return {status:r.status,data:await r.json()};};
 assert.equal((await call('/demo/login',{body:{account:'lin'}})).status,404);
 assert.equal((await call('/desk/login',{body:{account:'employee-7'}})).status,404);
 assert.equal((await call('/host/me')).data.demo,false);
 assert.equal((await call('/desk/queue')).status,401);
 assert.equal((await call('/host/chat-session',{cookie:'business=staff',body:{}})).status,401);
 const opened=await call('/host/chat-session',{body:{}});assert.equal(opened.status,200);
 assert.equal((await call('/chat/v1/messages',{token:cap})).status,200);
 hook=()=>{active=false;};
 assert.equal((await call('/chat/v1/messages',{token:cap})).status,401);
 const count=calls;assert.equal((await call('/chat/v1/messages',{token:cap})).status,401);assert.equal(calls,count);
 hook=()=>{staffActive=false;};
 assert.equal((await call('/desk/queue',{cookie:'business=staff'})).status,401);
 assert.equal((await call('/desk/me',{cookie:'business=staff'})).data.agent,null);
});
test('authentication service errors are sanitized',async t=>{
 const adapter=await startAdapter({gatewayOrigin:'http://127.0.0.1:1',hostSecret:'x'.repeat(43),identity:{customer:()=>{throw Object.assign(Error('private password'),{code:'private-secret'});}}});t.after(()=>adapter.close());
 const r=await fetch(adapter.origin+'/host/me');assert.equal(r.status,503);assert.deepEqual(await r.json(),{code:'authentication_unavailable'});
});
