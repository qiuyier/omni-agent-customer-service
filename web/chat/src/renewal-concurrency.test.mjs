import test from 'node:test';
import assert from 'node:assert/strict';
import {createChatAPI} from './protocol.mjs';
const response=(data={},status=200)=>new Response(JSON.stringify(data),{status});
const session=(token='old',conversation_id='c1')=>({access_token:token,conversation_id,user:{id:'alice'}});
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
const tick=()=>new Promise(r=>setImmediate(r));
function lab(){
 const renewal=deferred(),calls=[];let opens=0;
 const api=createChatAPI(async(path,options)=>{
  calls.push({path,...options});
  if(path==='/host/chat-session')return ++opens===1?response(session()):renewal.promise.then(r=>r.clone());
  return response({},options.headers.Authorization==='Bearer old'?401:200);
 });
 return {api,renewal,calls,opens:()=>opens};
}
test('concurrent history and send share one renewal and preserve message body',async()=>{
 const s=lab();await s.api.open();
 const pending={event_id:'same-event',content:'original'};
 const both=Promise.all([s.api.history(),s.api.send(pending)]);
 await tick();const count=s.opens();s.renewal.resolve(response(session('new')));
 await both;assert.equal(count,2);
 const sends=s.calls.filter(x=>x.path==='/chat/v1/messages'&&x.method==='POST');
 assert.equal(sends.length,2);assert.equal(sends[0].body,sends[1].body);
});
test('late old-token 401 reuses already renewed token without another acquisition',async()=>{
 const late=deferred();let opens=0;const calls=[];
 const api=createChatAPI(async(path,options)=>{
  calls.push({path,...options});
  if(path==='/host/chat-session')return response(session(++opens===1?'old':'new'));
  if(options.headers.Authorization==='Bearer old')return options.method==='GET'?late.promise:response({},401);
  return response();
 });
 await api.open();const history=api.history();await api.send({event_id:'e',content:'x'});
 late.resolve(response({},401));await history;assert.equal(opens,2);
});
test('aborting one waiter does not abort the other renewal waiter',async()=>{
 const s=lab(),controller=new AbortController();await s.api.open();
 const first=assert.rejects(s.api.history(controller.signal),{name:'AbortError'});
 const second=s.api.send({event_id:'e',content:'x'});await tick();controller.abort();
 await tick();s.renewal.resolve(response(session('new')));await Promise.all([first,second]);
 assert.equal(s.opens(),2);
 assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages'&&x.method==='GET').length,1);
});
test('clear during renewal cancels acquisition and prevents late replay',async()=>{
 const s=lab();await s.api.open();
 const result=assert.rejects(s.api.send({event_id:'e',content:'x'}),{name:'AbortError'});
 await tick();s.api.clear();
 const renewalCall=s.calls.filter(x=>x.path==='/host/chat-session')[1];
 s.renewal.resolve(response(session('new')));await result;
 assert.equal(renewalCall.signal.aborted,true);
 assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,1);
 await assert.rejects(s.api.history(),{code:'unauthorized'});
});
test('all cancelled waiters revoke abandoned renewal even if transport responds late',async()=>{
 const s=lab(),controller=new AbortController();await s.api.open();
 const result=assert.rejects(s.api.history(controller.signal),{name:'AbortError'});
 await tick();controller.abort();await tick();
 const call=s.calls.filter(x=>x.path==='/host/chat-session')[1];
 s.renewal.resolve(response(session('late')));await result;
 assert.equal(call.signal.aborted,true);
 assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,1);
});
test('shared renewal into a different conversation never replays either operation',async()=>{
 const s=lab();await s.api.open();
 const a=assert.rejects(s.api.history(),{code:'account_changed'});
 const b=assert.rejects(s.api.send({event_id:'e',content:'x'}),{code:'account_changed'});
 await tick();s.renewal.resolve(response(session('new','c2')));await Promise.all([a,b]);
 assert.equal(s.opens(),2);assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,2);
});

test('old renewal cannot overwrite a newly opened account after clear',async()=>{
 const old=deferred();let opens=0;const calls=[];
 const api=createChatAPI(async(path,options)=>{
  calls.push({path,...options});
  if(path==='/host/chat-session'){
   opens++;if(opens===2)return old.promise;
   return response(session(opens===1?'old':'new-account',opens===1?'c1':'c2'));
  }
  return response({},options.headers.Authorization==='Bearer old'?401:200);
 });
 await api.open();const result=assert.rejects(api.history(),{name:'AbortError'});
 await tick();api.clear();await result;await api.open();
 old.resolve(response(session('stale','c1')));await tick();await api.history();
 assert.equal(calls.at(-1).headers.Authorization,'Bearer new-account');
});
test('shared renewal rejection stops all callers without message replay',async()=>{
 const s=lab();await s.api.open();
 const results=Promise.all([assert.rejects(s.api.history(),{code:'unauthorized'}),assert.rejects(s.api.send({event_id:'e',content:'x'}),{code:'unauthorized'})]);
 await tick();s.renewal.resolve(response({},401));await results;
 assert.equal(s.opens(),2);assert.equal(s.calls.filter(x=>x.path==='/chat/v1/messages').length,2);
});
test('concurrent duplicate sends retain the same event and do not invent retries',async()=>{
 const s=lab();await s.api.open();const pending={event_id:'one-event',content:'same content'};
 const result=Promise.all([s.api.send(pending),s.api.send(pending)]);
 await tick();s.renewal.resolve(response(session('new')));await result;
 assert.equal(s.opens(),2);
 const sends=s.calls.filter(x=>x.path==='/chat/v1/messages');
 assert.equal(sends.length,4);assert.deepEqual([...new Set(sends.map(x=>x.body))],[JSON.stringify(pending)]);
});
test('failed shared renewal releases its slot for a later explicit retry',async()=>{
 let opens=0;const failed=deferred();
 const api=createChatAPI(async(path,options)=>{
  if(path==='/host/chat-session'){
   opens++;return opens===2?failed.promise:response(session(opens===1?'old':'new'));
  }
  return response({},options.headers.Authorization==='Bearer old'?401:200);
 });
 await api.open();const first=assert.rejects(api.history(),{code:'unavailable'});
 await tick();failed.resolve(response({},503));await first;
 await api.history();assert.equal(opens,3);
});
