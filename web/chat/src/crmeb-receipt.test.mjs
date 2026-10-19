import test from 'node:test';
import assert from 'node:assert/strict';
import {createCRMEBAPI} from './crmeb.mjs';
import {reconcilePending} from './pending-receipt.mjs';
import {savePending,readPending} from './protocol.mjs';
const s={access_token:'chat_'+'x'.repeat(43),conversation_id:'wbc_'+'a'.repeat(64),expires_at:new Date(Date.now()+300000).toISOString()};
const response=(x,status=200)=>new Response(JSON.stringify(x),{status,headers:{'Content-Type':'application/json'}});
const pending={event_id:'11111111-1111-4111-a111-111111111111',content:'receipt test'};
const storage=()=>{const m=new Map();return {getItem:k=>m.get(k)??null,setItem:(k,v)=>m.set(k,v),removeItem:k=>m.delete(k)};};
test('CRMEB receipt renews only its query, never sends, and clears exact cache',async()=>{
 const paths=[],store=storage();savePending(store,s.conversation_id,pending);
 const replies=[response(s),response({},401),response(s),response({conversation_id:s.conversation_id,event_id:pending.event_id,accepted:true,message_id:'m'})];
 const api=createCRMEBAPI({getLogin:()=> 'a.b.c',fetcher:async(p,o)=>{paths.push(p);if(p.endsWith('message-receipt')){assert.deepEqual(JSON.parse(o.body),pending);assert.equal(o.headers['Authori-zation'],'Bearer a.b.c');assert.equal(o.headers['X-Omni-Chat-Token'],s.access_token);}return replies.shift();}});
 await api.open();assert.equal(await reconcilePending({api,storage:store,conversationID:s.conversation_id,pending,active:()=>true}),true);
 assert.equal(readPending(store,s.conversation_id),null);
 assert.deepEqual(paths,['/api/omni-chat/session','/api/omni-chat/message-receipt','/api/omni-chat/session','/api/omni-chat/message-receipt']);
});
for(const status of [200,401,503])test('late CRMEB receipt after account change preserves cache '+status,async()=>{
 let login='a.b.c',finish;const store=storage();savePending(store,s.conversation_id,pending);
 const paths=[];const api=createCRMEBAPI({getLogin:()=>login,fetcher:async(p)=>{paths.push(p);return p.endsWith('session')?response(s):new Promise(r=>finish=r);}});
 await api.open();const task=reconcilePending({api,storage:store,conversationID:s.conversation_id,pending,active:()=>true});
 login='d.e.f';api.clear();finish(response({conversation_id:s.conversation_id,event_id:pending.event_id,accepted:true,message_id:'m'},status));
 await assert.rejects(task,{name:'AbortError'});assert.deepEqual(readPending(store,s.conversation_id),{version:1,conversation_id:s.conversation_id,...pending});assert.equal(paths.length,2);
});
