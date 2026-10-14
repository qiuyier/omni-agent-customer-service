import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {acceptHost,acceptanceExitCode} from './acceptance.mjs';
const input=()=>({preflight:{origin:'http://127.0.0.1:12345',paths:{customerIdentity:'/customer/me',staffIdentity:'/staff/me'}},lifecycle:{customers:[{account:'a'},{account:'b'}],staff:{account:'s'}}});
const read=async()=>({status:'needs_verification',checks:[]});
test('default never invokes writes, even with login config supplied',async()=>{
 let writes=0;const r=await acceptHost(input(),{preflight:read,verify:async()=>{writes++;}});
 assert.equal(writes,0);assert.equal(r.mode,'read_only');assert.equal(acceptanceExitCode(r),2);
});
test('read failures block authorized writes',async()=>{
 let writes=0;const r=await acceptHost(input(),{allowTestWrites:true,preflight:async()=>({status:'fail',checks:[]}),verify:async()=>{writes++;}});
 assert.equal(writes,0);assert.equal(acceptanceExitCode(r),1);
});
test('authorized lifecycle reuses same target but cannot certify manual checks',async()=>{
 const config=input();let writes=0;
 const r=await acceptHost(config,{allowTestWrites:true,preflight:read,verify:async(c,o)=>{writes++;assert.equal(c.origin,config.preflight.origin);assert.equal(o.allowTestWrites,true);return {passed:true,checks:['close']};}});
 assert.equal(writes,1);assert.equal(r.checks.find(c=>c.id==='lifecycle').status,'pass');assert.equal(r.status,'needs_verification');
 for(const id of ['identity_transitions','browser','deployment'])assert.equal(r.checks.find(c=>c.id===id).status,'needs_verification');
});
test('cleanup failure is a failed acceptance',async()=>{
 const r=await acceptHost(input(),{allowTestWrites:true,preflight:read,verify:async()=>({passed:false,checks:['close'],cleanup_failures:1})});assert.equal(r.status,'fail');assert.equal(r.phases.lifecycle.cleanup_failures,1);
});
test('invalid configuration, different target or paths fail before any request',async()=>{
 for(const change of [c=>c.lifecycle.origin='http://127.0.0.1:2222',c=>c.lifecycle.paths={me:'/different'},c=>c.lifecycle.paths={messages:'//invalid'},c=>c.lifecycle.customers=[],c=>c.evidence={all:'pass'}]){
  const c=input();change(c);let reads=0;const r=await acceptHost(c,{preflight:async()=>{reads++;}});assert.equal(reads,0);assert.equal(r.status,'fail');
 }
});
test('missing lifecycle is explicitly unverified and exceptions are sanitized',async()=>{
 const c=input();delete c.lifecycle;const r=await acceptHost(c,{allowTestWrites:true,preflight:read});assert.equal(r.status,'needs_verification');
 for(const failRead of [true,false]){const r=await acceptHost(input(),{allowTestWrites:true,preflight:failRead?async()=>{throw Error('secret-password');}:read,verify:async()=>{throw Error('secret-password');}});assert.equal(r.status,'fail');assert.ok(!JSON.stringify(r).includes('secret-password'));}
});
test('CLI rejects unknown flags and malformed stdin without echoing credentials',()=>{
 for(const [args,body] of [[['--password=secret-password'],'{}'],[[],'secret-password']]){
  const r=spawnSync(process.execPath,[new URL('./acceptance.mjs',import.meta.url).pathname,...args],{input:body,encoding:'utf8'});assert.equal(r.status,1);assert.equal(JSON.parse(r.stdout).status,'fail');assert.ok(!r.stdout.includes('secret-password'));
 }
});
