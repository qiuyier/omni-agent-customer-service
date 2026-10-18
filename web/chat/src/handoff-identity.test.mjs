import test from 'node:test';
import assert from 'node:assert/strict';
import {createChatAPI,savePending,readPending} from './protocol.mjs';
const response=(data,status=200)=>new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json'}});
const session=id=>({access_token:'token-'+id,conversation_id:'conversation-'+id,user:{id},reply_mode:'ai',human_enabled:true});

for(const code of [200,401,503])for(const order of ['send-first','history-first']){
 test('handoff account switch rejects both old flights: '+code+' '+order,async()=>{
  let account='a';const held={},calls=[];
  const api=createChatAPI(async(path,options)=>{
   calls.push({path,method:options.method,token:options.headers.Authorization});
   if(path==='/host/chat-session')return response(session(account));
   if(options.headers.Authorization==='Bearer token-a')return new Promise(resolve=>{held[options.method]=resolve;});
   return response({conversation_id:'conversation-b',status:'bot_active',messages:[]});
  });
  await api.open();
  const send=api.send({event_id:'old-event',content:'A private question'});
  const history=api.history();
  // Attach rejection handlers before resolving either delayed transport.
  const sendRejected=assert.rejects(send,{name:'AbortError'});
  const historyRejected=assert.rejects(history,{name:'AbortError'});
  api.clear();account='b';await api.open();
  const old=code===200?{conversation_id:'conversation-a',outcome:'superseded',message_id:'a-only',messages:[{content:'A private human reply'}]}:{error:code===401?'unauthorized':'unavailable'};
  for(const method of order==='send-first'?['POST','GET']:['GET','POST'])held[method](response(old,code));
  await Promise.all([sendRejected,historyRejected]);
  assert.deepEqual(await api.history(),{conversation_id:'conversation-b',status:'bot_active',messages:[]});
  assert.equal(calls.filter(c=>c.path==='/host/chat-session').length,2,'old 401 must not renew');
  assert.equal(calls.filter(c=>c.path==='/chat/v1/messages'&&c.method==='POST').length,1,'no resend');
  assert.equal(calls.at(-1).token,'Bearer token-b');
 });
}
test('reload reads closed human history while retaining only its own uncertain event',async()=>{
 const values=new Map();
 const storage={setItem:(k,v)=>values.set(k,v),getItem:k=>values.get(k),removeItem:k=>values.delete(k)};
 const pending={event_id:'2c8b50a1-0500-4000-9000-361942851810',content:'A question'};
 savePending(storage,'conversation-a',pending);
 assert.equal(readPending(storage,'conversation-b'),null);
 const calls=[];
 const api=createChatAPI(async(path,options)=>{
  calls.push({path,method:options.method});
  return response(path==='/host/chat-session'?session('a'):{conversation_id:'conversation-a',status:'closed',messages:[{id:'customer',role:'customer',content:'A question'},{id:'staff',role:'human_agent',content:'A reply'}]});
 });
 await api.open();
 assert.equal((await api.history()).status,'closed');
 assert.equal(readPending(storage,'conversation-a').event_id,pending.event_id);
 assert.deepEqual(calls,[{path:'/host/chat-session',method:'POST'},{path:'/chat/v1/messages',method:'GET'}]);
});
