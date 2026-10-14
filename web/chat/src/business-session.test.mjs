import test from 'node:test';
import assert from 'node:assert/strict';
import {createPinnedFetch} from './business-session.mjs';
test('business requests pin the login version without replacing bearer headers',async()=>{
  let seen;const fetcher=createPinnedFetch({version:'nonce',isCurrent:()=>true,onUnauthorized:()=>{},fetcher:async(path,options)=>{seen=options.headers;return new Response('{}');}});
  await fetcher('/host/me',{headers:{Authorization:'Bearer private'}});
  assert.equal(seen.get('X-Business-Session'),'nonce');assert.equal(seen.get('Authorization'),'Bearer private');
});
test('invalidated views cannot send or accept late results, including late 401',async()=>{
  let current=false,calls=0,unauthorized=0,finish;
  const fetcher=createPinnedFetch({version:'nonce',isCurrent:()=>current,onUnauthorized:()=>unauthorized++,fetcher:()=>{calls++;return new Promise(r=>finish=r);}});
  await assert.rejects(fetcher('/desk/action'),{name:'AbortError'});assert.equal(calls,0);
  current=true;const pending=fetcher('/desk/queue');current=false;finish(new Response('{}',{status:401}));
  await assert.rejects(pending,{name:'AbortError'});assert.equal(unauthorized,0);
});
test('an active unauthorized response invalidates the view',async()=>{
  let calls=0;const fetcher=createPinnedFetch({version:'nonce',isCurrent:()=>true,onUnauthorized:()=>calls++,fetcher:async()=>new Response('{}',{status:401})});
  assert.equal((await fetcher('/desk/queue')).status,401);assert.equal(calls,1);
});
