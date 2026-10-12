import test from 'node:test';
import assert from 'node:assert/strict';
import {createReadReceipts} from './read-receipts.mjs';
function fixture(send=async()=>{}){
 let visible=true,active=true;const timers=new Map(),sent=[];let n=0,errors=0;
 const tracker=createReadReceipts({send:async ids=>{sent.push(ids);await send(ids);},visible:()=>visible,active:()=>active,onError:()=>errors++,schedule:fn=>{timers.set(++n,fn);return n;},cancel:id=>timers.delete(id)});
 return {tracker,sent,get errors(){return errors;},setVisible:v=>visible=v,setActive:v=>active=v,async tick(){const list=[...timers.values()];timers.clear();for(const fn of list)await fn();}};
}
test('only currently visible IDs; hidden page/history polling cannot mark read',async()=>{
 const f=fixture();await f.tick();assert.deepEqual(f.sent,[]);
 f.tracker.intersection('offscreen',true);f.tracker.intersection('offscreen',false);
 f.tracker.intersection('first',true);f.setVisible(false);await f.tick();assert.deepEqual(f.sent,[]);
 f.setVisible(true);f.tracker.visibility();await f.tick();assert.deepEqual(f.sent,[['first']]);
 f.tracker.intersection('first',true);await f.tick();assert.equal(f.sent.length,1);
});
test('new arrival while acknowledging stays separate and requests never overlap',async()=>{
 let release;const f=fixture(()=>new Promise(r=>release=r));
 f.tracker.intersection('old',true);const first=f.tick();
 f.tracker.intersection('new',true);await f.tick();assert.deepEqual(f.sent,[['old']]);
 release();await first;const second=f.tick();assert.deepEqual(f.sent,[['old'],['new']]);release();await second;
});
test('context invalidation and cleanup prevent stale acknowledgements',async()=>{
 const f=fixture();f.tracker.intersection('old',true);f.setActive(false);await f.tick();assert.deepEqual(f.sent,[]);
 f.setActive(true);f.tracker.visibility();f.tracker.stop();await f.tick();assert.deepEqual(f.sent,[]);
});
test('failure retains unread and stops automatic retry storm',async()=>{
 const f=fixture(async()=>{throw Error('offline');});f.tracker.intersection('one',true);await f.tick();assert.equal(f.errors,1);
 f.tracker.visibility();f.tracker.intersection('two',true);await f.tick();assert.equal(f.sent.length,1);
});
test('batch size capped at 100 without losing remaining visible messages',async()=>{
 const f=fixture();for(let i=0;i<105;i++)f.tracker.intersection(String(i),true);await f.tick();await f.tick();assert.deepEqual(f.sent.map(v=>v.length),[100,5]);
});
