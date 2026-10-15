import test from 'node:test';
import assert from 'node:assert/strict';
import {createHostSessionConfig,readHostIdentity,announceHostIdentityChange,createPinnedFetch} from './host-session.mjs';
const input={role:'customer',identityURL:'/portal/current',logoutURL:'/portal/leave',loginURL:'/sign-in',channelName:'portal-auth'};
test('host config supports unrelated routes without repair-specific defaults',()=>{
  const config=createHostSessionConfig(input);assert.ok(Object.isFrozen(config));assert.equal(config.identityURL,'/portal/current');
  const staff=createHostSessionConfig({...input,role:'staff',identityURL:'/employees/current'});assert.equal(staff.role,'staff');
});
test('missing, external or ambiguous paths and unsupported roles fail closed',()=>{
  for(const value of [undefined,'https://elsewhere.invalid','//elsewhere.invalid','/../admin','/a\\b','/a?redirect=x','/a#x'])assert.throws(()=>createHostSessionConfig({...input,identityURL:value}));
  assert.throws(()=>createHostSessionConfig({...input,role:'admin'}));assert.throws(()=>createHostSessionConfig({...input,channelName:''}));
});
test('public identity is role-specific and strips all unrelated fields',()=>{
  const identity={id:'member_3',role:'customer',version:'v'.repeat(32),token:'must-not-forward'};
  assert.deepEqual(readHostIdentity({identity},'customer'),{id:identity.id,role:identity.role,version:identity.version});
  assert.equal(readHostIdentity({identity:null},'customer'),null);
  assert.throws(()=>readHostIdentity({identity},'staff'));
  for(const data of [{},{identity:{}},{identity:{...identity,version:''}},{identity:{...identity,id:'../x'}}])assert.throws(()=>readHostIdentity(data,'customer'));
});
test('account notifications contain only realm, use configured channel and always close',()=>{
  const seen=[];class Channel{constructor(name){seen.push(name);}postMessage(value){seen.push(value);}close(){seen.push('closed');}}
  announceHostIdentityChange(input,Channel);assert.deepEqual(seen,['portal-auth',{role:'customer'},'closed']);
  class Broken extends Channel{postMessage(){throw Error('blocked');}}
  announceHostIdentityChange(input,Broken);assert.equal(seen.at(-1),'closed');assert.doesNotThrow(()=>announceHostIdentityChange(input,null));
});
test('host request wrapper refuses external destinations and redirects',async()=>{
  let seen,calls=0;const fetcher=createPinnedFetch({version:'v'.repeat(32),isCurrent:()=>true,onUnauthorized:()=>{},fetcher:async(path,options)=>{calls++;seen=options;return new Response('{}');}});
  for(const path of ['https://elsewhere.invalid','//elsewhere.invalid','/\\elsewhere.invalid','/\t/elsewhere.invalid'])await assert.rejects(fetcher(path));assert.equal(calls,0);
  await fetcher('/desk/queue',{redirect:'follow',credentials:'include'});assert.equal(seen.redirect,'error');assert.equal(seen.credentials,'same-origin');
});
test('late successful history response cannot enter a replacement identity',async()=>{
 let current=true,finish,unauthorized=0;
 const fetcher=createPinnedFetch({version:'old-version',isCurrent:()=>current,onUnauthorized:()=>unauthorized++,fetcher:()=>new Promise(resolve=>finish=resolve)});
 const pending=fetcher('/chat/v1/messages');current=false;
 finish(new Response(JSON.stringify({messages:[{content:'old-account-only'}]})));
 await assert.rejects(pending,{name:'AbortError'});assert.equal(unauthorized,0);
});
