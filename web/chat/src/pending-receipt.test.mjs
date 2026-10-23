import test from 'node:test';
import assert from 'node:assert/strict';
import {savePending,readPending} from './protocol.mjs';
import {reconcilePending,probeAccepted} from './pending-receipt.mjs';
import {createPoller} from './polling.mjs';
import {readFileSync} from 'node:fs';
const pending={event_id:'2c8b50a1-0500-4000-9000-361942851810',content:'original'};
const good={conversation_id:'a',event_id:pending.event_id,accepted:true,message_id:'m-1',reply_status:'replied',retry_allowed:false};
function fixture(){
 const map=new Map();const storage={getItem:k=>map.get(k),setItem:(k,v)=>map.set(k,v),removeItem:k=>map.delete(k)};
 savePending(storage,'a',pending);savePending(storage,'b',pending);
 return {storage,pending,conversationID:'a',active:()=>true};
}
test('only exact acceptance clears its own cache, never sends',async()=>{
 const f=fixture();let calls=0;
 assert.equal(await reconcilePending({...f,api:{receipt:async p=>{calls++;assert.deepEqual(p,pending);return good;}}}),true);
 assert.equal(calls,1);assert.equal(readPending(f.storage,'a'),null);assert.ok(readPending(f.storage,'b'));
});
test('unknown, mismatched, malformed and missing receipts retain cache',async()=>{
 for(const result of [{...good,accepted:false},{...good,accepted:'true'},{...good,conversation_id:'b'},{...good,event_id:'other'},{...good,message_id:''},{}]){
  const f=fixture();assert.equal(await reconcilePending({...f,api:{receipt:async()=>result}}),false);assert.ok(readPending(f.storage,'a'));
 }
});
test('failure or storage denial never loses the pending event',async()=>{
 for(const fail of ['network','storage']){
  const f=fixture();if(fail==='storage')f.storage.removeItem=()=>{throw Error('denied');};
  await assert.rejects(reconcilePending({...f,api:{receipt:async()=>{if(fail==='network')throw Error('offline');return good;}}}));
  assert.ok(readPending(f.storage,'a'));
 }
});
test('late receipt cannot clear a replacement identity or a newer pending event',async()=>{
 for(const change of ['identity','event','abort']){
  const f=fixture();let finish,current=true;const controller=new AbortController();
  const task=reconcilePending({...f,active:()=>current,signal:controller.signal,api:{receipt:()=>new Promise(r=>finish=r)}});
  if(change==='identity')current=false;
  if(change==='abort')controller.abort();
  if(change==='event')savePending(f.storage,'a',{...pending,event_id:'3c8b50a1-0500-4000-9000-361942851810'});
  finish(good);assert.equal(await task,false);assert.ok(readPending(f.storage,'a'));
 }
});


test('send failure acceptance probe never clears pending or generates a reply',async()=>{
 const f=fixture();let calls=0;const api={receipt:async()=>{calls++;return good;},send:()=>{throw Error('must not resend');}};
 assert.equal(await probeAccepted({...f,api}),true);assert.equal(calls,1);assert.ok(readPending(f.storage,'a'));
});
test('acceptance probe rejects mismatches and late identity changes',async()=>{
 for(const value of [{...good,accepted:false},{...good,conversation_id:'b'},{...good,event_id:'other'},{...good,message_id:''},{}])assert.equal(await probeAccepted({...fixture(),api:{receipt:async()=>value}}),false);
 let current=true;const f=fixture();assert.equal(await probeAccepted({...f,active:()=>current,api:{receipt:async()=>{current=false;return good;}}}),false);
});


test('reload retains accepted unanswered event and never sends',async()=>{
 const f=fixture();let state;const result={...good,reply_status:'not_replied',retry_allowed:true};
 assert.equal(await reconcilePending({...f,onReplyState:s=>state=s,api:{receipt:async()=>result,send:()=>{throw Error('unexpected send');}}}),false);
 assert.equal(state,'retry');assert.deepEqual(readPending(f.storage,'a').event_id,pending.event_id);
});
test('unknown reply contracts retain event without granting retry',async()=>{
 for(const fields of [{reply_status:undefined,retry_allowed:undefined},{reply_status:'not_replied',retry_allowed:false},{reply_status:'replied',retry_allowed:true},{reply_status:'not_replied',retry_allowed:'true'}]){
  const f=fixture();let state;assert.equal(await reconcilePending({...f,onReplyState:s=>state=s,api:{receipt:async()=>({...good,...fields})}}),false);assert.equal(state,'unknown');assert.ok(readPending(f.storage,'a'));
 }
});
test('handoff closure and superseded receipt clear only local recovery state',async()=>{
 const f=fixture();let state;assert.equal(await reconcilePending({...f,onReplyState:s=>state=s,api:{receipt:async()=>({...good,reply_status:'unavailable',retry_allowed:false})}}),true);assert.equal(state,'unavailable');assert.equal(readPending(f.storage,'a'),null);assert.ok(readPending(f.storage,'b'));
});

test('background probes revoke previously allowed retry without generating',async()=>{
 for(const terminal of ['replied','unavailable']){
  const f=fixture();let receipt={...good,reply_status:'not_replied',retry_allowed:true},next,state,calls=0;
  const api={receipt:async()=>{calls++;return receipt;},send:()=>assert.fail('poll must not send')};
  const poller=createPoller({schedule:fn=>{next=fn;return 1;},unschedule:()=>{},read:async task=>{
   await reconcilePending({...f,api,...task,onReplyState:s=>state=s});
  }});
  await next();assert.equal(state,'retry');assert.ok(readPending(f.storage,'a'));
  receipt={...good,reply_status:terminal,retry_allowed:false};
  await next();assert.equal(state,terminal);assert.equal(readPending(f.storage,'a'),null);assert.equal(calls,2);poller.stop();
 }
});
test('invalidated background probe cannot clear pending or restore retry eligibility',async()=>{
 for(const reply_status of ['replied','not_replied']){
  const f=fixture();let next,finish;const states=[];
  const poller=createPoller({schedule:fn=>{next=fn;return 1;},unschedule:()=>{},read:task=>reconcilePending({...f,...task,onReplyState:s=>states.push(s),api:{receipt:()=>new Promise(r=>finish=r)}})});
  const work=next();poller.invalidate();finish({...good,reply_status,retry_allowed:reply_status==='not_replied'});await work;
  assert.deepEqual(states,[]);assert.ok(readPending(f.storage,'a'));poller.stop();
 }
});
test('customer polling wires idle receipt reconciliation with operation-scoped validity',()=>{
 const source=readFileSync(new URL('./app.jsx',import.meta.url),'utf8');
 assert.match(source,/version===operation.current.id&&round.current===owner/);
 assert.match(source,/if\(active\(\)&&!sending&&pending\)await confirmPending\(\{signal:task.signal,active\},owner,pending\)/);
 assert.match(source,/setReplyRetryAllowed\(false\);\s*setNotice\('暂未能核实/);
 assert.match(source,/confirmPending\(task,session.conversation_id,outgoing,e.code==='busy'\)/);
});

