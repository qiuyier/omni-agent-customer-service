import test from 'node:test';
import assert from 'node:assert/strict';
import { createCRMEBAPI, readLogin, eventID, replyDescription } from './crmeb.mjs';
const loginA = 'aaa.bbb.ccc', loginB = 'ddd.eee.fff';
const s = (id = 'a', token = 'x') => ({access_token:'chat_'+token.repeat(43),conversation_id:'wbc_'+id.repeat(64),expires_at:new Date(Date.now()+300000).toISOString()});
const reply = (data, status = 200) => new Response(JSON.stringify(data), {status, headers:{'Content-Type':'application/json'}});
test('AI disclosure is explicit, unknown modes never imply AI, old replies not relabeled',()=>{
 assert.match(replyDescription('ai'),/对话内容将用于模型生成/);
 assert.match(replyDescription('ai'),/历史记录可能包含/);
 assert.match(replyDescription('fake'),/不调用模型/);
 assert.match(replyDescription('other'),/未确认/);
});
test('AI failure remains retryable with the exact event and no automatic replay',async()=>{
 const calls=[],responses=[reply({...s(),reply_mode:'ai'}),reply({error:'unavailable'},503),reply({message_id:'m1'})];
 const api=createCRMEBAPI({getLogin:()=>loginA,fetcher:async(p,o)=>{calls.push([p,o]);return responses.shift();}});
 await api.open();const pending={event_id:'ai-retry',content:'same content'};
 await assert.rejects(api.send(pending),{status:503});assert.equal(calls.length,2);
 await api.send(pending);assert.equal(calls[1][1].body,calls[2][1].body);
});
test('deployment metadata defaults to unknown and accepts only known values',async()=>{
 for (const [metadata, expected] of [[{},['unknown','unknown']],[{storage:'postgres',reply_mode:'fake'},['postgres','fake']],[{storage:'unexpected',reply_mode:'unexpected'},['unknown','unknown']]]) {
  const api=createCRMEBAPI({getLogin:()=>loginA,fetcher:async()=>reply({...s(),...metadata})});
  const opened=await api.open();assert.deepEqual([opened.storage,opened.reply_mode],expected);
 }
});
test('CRMEB raw string token only, storage denial fails closed',()=>{
 assert.equal(readLogin({getItem:()=>loginA}),loginA);
 assert.equal(readLogin({getItem:()=>JSON.stringify({data:loginA})}),'');
 assert.equal(readLogin({getItem:()=>{throw Error('blocked')}}),'');
 assert.match(eventID(), /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/);
});
test('credentials stay separate, pinned, and out of returned session',async()=>{
 const calls=[]; const api=createCRMEBAPI({getLogin:()=>loginA,fetcher:async(p,o)=>{calls.push([p,o]);return reply(p.endsWith('session')?s():{messages:[]});}});
 const opened=await api.open(); await api.history();
 assert.equal(opened.access_token,undefined);assert.equal(calls[0][1].headers['Authori-zation'],'Bearer '+loginA);
 assert.equal(calls[0][1].headers.Authorization,undefined);assert.equal(calls[0][1].headers['X-Omni-Chat-Token'],undefined);
 assert.equal(calls[1][1].headers['X-Omni-Chat-Token'],s().access_token);assert.equal(calls[1][1].redirect,'error');
});
test('account changes reject old requests before networking',async()=>{
 let login=loginA,calls=0;const api=createCRMEBAPI({getLogin:()=>login,fetcher:async()=>{calls++;return reply(s());}});
 await api.open();login=loginB;await assert.rejects(api.history(),{code:'account_changed'});assert.equal(calls,1);
});
test('late results cannot restore cleared or switched identity',async()=>{
 let finish,login=loginA;const api=createCRMEBAPI({getLogin:()=>login,fetcher:()=>new Promise(r=>finish=r)});
 const first=api.open();api.clear();finish(reply(s()));await assert.rejects(first,{name:'AbortError'});
 const second=api.open();login=loginB;finish(reply(s()));await assert.rejects(second,{code:'account_changed'});
});
test('expired capability renews once and reuses exact pending identity',async()=>{
 const responses=[reply(s()),reply({},401),reply(s('a','y')),reply({message_id:'m1'})],calls=[];
 const api=createCRMEBAPI({getLogin:()=>loginA,fetcher:async(p,o)=>{calls.push([p,o]);return responses.shift();}});
 await api.open();await api.send({event_id:'same-event',content:'content'});
 assert.equal(calls[1][1].body,calls[3][1].body);assert.equal(calls[3][1].headers['X-Omni-Chat-Token'],s('a','y').access_token);
});
test('renewal into different conversation never resends',async()=>{
 const responses=[reply(s()),reply({},401),reply(s('b'))];let calls=0;
 const api=createCRMEBAPI({getLogin:()=>loginA,fetcher:async()=>{calls++;return responses.shift();}});
 await api.open();await assert.rejects(api.send({event_id:'same-event',content:'secret'}),{code:'account_changed'});assert.equal(calls,3);
});
test('uncertain network failures are not automatically replayed',async()=>{
 let calls=0;const api=createCRMEBAPI({getLogin:()=>loginA,fetcher:async()=>{if(++calls===1)return reply(s());throw Error('offline');}});
 await api.open();await assert.rejects(api.send({event_id:'e',content:'text'}),{code:'unavailable'});assert.equal(calls,2);
});
test('late 401 after identity replacement cannot renew or resend',async()=>{
 let login=loginA,complete,calls=0;
 const api=createCRMEBAPI({getLogin:()=>login,fetcher:async()=>{
  ++calls;if(calls===1)return reply(s());if(calls===2)return new Promise(r=>complete=r);return reply(s('b'));
 }});
 await api.open();const old=api.send({event_id:'old-event',content:'old content'});
 login=loginB;await api.open();complete(reply({},401));
 await assert.rejects(old,{name:'AbortError'});assert.equal(calls,3);
});
