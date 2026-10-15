import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {acceptHost} from './acceptance.mjs';
import {businessIdentity} from './identity.example.mjs';
import {startAdapter} from './adapter.mjs';
const sample=async name=>JSON.parse(await readFile(new URL('./starter/'+name+'.example.json',import.meta.url),'utf8'));
test('starter inputs compose with acceptance without implicitly authorizing writes',async()=>{
 for(const name of ['preflight','acceptance']){
  let reads=0,writes=0;
  const report=await acceptHost(await sample(name),{preflight:async c=>{reads++;assert.equal(c.paths.customerIdentity,'/auth/customer/me');return {status:'needs_verification',checks:[]};},verify:async()=>{writes++;}});
  assert.equal(reads,1);assert.equal(writes,0);assert.equal(report.status,'needs_verification');
 }
});
test('starter authorized template uses existing lifecycle and retains manual evidence gaps',async()=>{
 const config=await sample('acceptance');let calls=0;
 const report=await acceptHost(config,{allowTestWrites:true,preflight:async()=>({status:'needs_verification',checks:[]}),verify:async c=>{calls++;assert.equal(c.origin,config.preflight.origin);assert.deepEqual(c.customers,config.lifecycle.customers);assert.equal(c.paths.staffLogin,'/auth/staff/login');return {passed:true};}});
 assert.equal(calls,1);assert.equal(report.phases.lifecycle.passed,true);assert.equal(report.status,'needs_verification');
 for(const id of ['browser','deployment','identity_transitions'])assert.equal(report.checks.find(c=>c.id===id).status,'needs_verification');
});
test('documented identity template works on real HTTP adapter and fails closed after revocation',async t=>{
 let record={subjectID:'customer_a',displayName:'测试客户',role:'customer',enabled:true,sessionID:'opaque-business-session-001',expiresAt:Date.now()+60000};
 const sessions={authenticate:async(req,role)=>req.headers.cookie==='business=verified'&&role==='customer'?record:null};
 const adapter=await startAdapter({gatewayOrigin:'http://127.0.0.1:1',hostSecret:'h'.repeat(43),identity:businessIdentity({sessions})});t.after(()=>adapter.close());
 const me=async()=>{const r=await fetch(adapter.origin+'/host/me',{headers:{Cookie:'business=verified'}});assert.equal(r.status,200);return r.json();};
 const valid=await me();assert.equal(valid.user.id,'customer_a');assert.equal(valid.demo,false);
 for(const invalid of [{...record,enabled:false},{...record,role:'staff'},{...record,expiresAt:0},null]){record=invalid;assert.equal((await me()).user,null);}
 assert.equal((await fetch(adapter.origin+'/demo/login',{method:'POST',headers:{Origin:adapter.origin,'Content-Type':'application/json'},body:'{}'})).status,404);
});
