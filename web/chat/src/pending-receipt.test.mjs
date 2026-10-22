import test from 'node:test';
import assert from 'node:assert/strict';
import {savePending,readPending} from './protocol.mjs';
import {reconcilePending,probeAccepted} from './pending-receipt.mjs';
const pending={event_id:'2c8b50a1-0500-4000-9000-361942851810',content:'original'};
const good={conversation_id:'a',event_id:pending.event_id,accepted:true,message_id:'m-1'};
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

