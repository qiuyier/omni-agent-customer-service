import test from 'node:test';
import assert from 'node:assert/strict';
import {readDeskSearch,validSearchQuery} from './desk-search.mjs';
const page={items:[{conversation_id:'conv-z',status:'closed',revision:2}]};
test('search binds actor, uses POST body and checks identity before and after',async()=>{
 const calls=[];const request=async(p,o)=>{calls.push([p,o]);return p==='/desk/me'?{agent:{id:'staff'}}:page;};
 assert.deepEqual(await readDeskSearch(request,'staff',{keyword:'虚构',after:'conv-a'}),page);
 assert.deepEqual(calls.map(([p])=>p),['/desk/me','/desk/search','/desk/me']);
 assert.equal(calls[1][1].method,'POST');assert.equal(calls[1][1].body.expected_agent_id,'staff');
});
test('identity mismatch before search never sends query; mismatch after hides result',async()=>{
 let calls=0;await assert.rejects(readDeskSearch(async()=>{calls++;return {agent:{id:'other'}};},'staff',{}),e=>e.status===409);assert.equal(calls,1);
 let reads=0;await assert.rejects(readDeskSearch(async p=>p==='/desk/me'?{agent:{id:++reads===1?'staff':'other'}}:page,'staff',{}),e=>e.status===409);
});
test('malformed, oversized, unordered and looping pages are rejected',async()=>{
 for(const p of [{items:Array(21).fill(page.items[0])},{items:[{...page.items[0],status:'bot_active'}]},{...page,next:'wrong'},{items:[page.items[0],page.items[0]]}]){
  await assert.rejects(readDeskSearch(async path=>path==='/desk/me'?{agent:{id:'staff'}}:p,'staff',{}),/invalid_search/);
 }
});
test('filters enforce UTF-8 byte limits and literal wildcard input is allowed',()=>{
 const q={keyword:'%_',conversation_id:'suffix',status:'closed'};assert.ok(validSearchQuery(q));
 assert.equal(validSearchQuery({...q,keyword:'问'.repeat(86)}),false);
 assert.equal(validSearchQuery({...q,status:'bot_active'}),false);
 assert.equal(validSearchQuery({...q,keyword:'\0'}),false);
});
