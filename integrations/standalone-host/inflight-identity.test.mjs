import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {startHost} from './host.mjs';

for(const change of ['expire','switch'])test(`in-flight send suppresses late result after ${change}`,{timeout:15000},async t=>{
 let clock=Date.now(),release,entered,calls=0;
 const waiting=new Promise(r=>entered=r),gate=new Promise(r=>release=r);
 const owners=new Map(),stored=new Map();
 const upstream=http.createServer(async(req,res)=>{
  let text='';for await(const chunk of req)text+=chunk;const body=text?JSON.parse(text):{};
  res.setHeader('Content-Type','application/json');
  if(req.url==='/internal/chat/sessions'){
   const a=body.customer_id==='member-1001',token='chat_'+(a?'a':'b').repeat(43),id='wbc_'+(a?'a':'b').repeat(64);owners.set(token,id);
   res.end(JSON.stringify({access_token:token,conversation_id:id,expires_at:new Date(clock+300000).toISOString()}));return;
  }
  const id=owners.get(req.headers.authorization?.slice(7));assert.ok(id);
  if(req.method==='POST'){
   calls++;entered();await gate;stored.set(id,[{content:'A private question'},{content:'A private answer'}]);
   res.end(JSON.stringify({conversation_id:id,outcome:'replied',message_id:'customer-A',reply_message_id:'private-reply-A'}));return;
  }
  res.end(JSON.stringify({conversation_id:id,messages:stored.get(id)||[]}));
 });
 await new Promise(r=>upstream.listen(0,'127.0.0.1',r));
 t.after(()=>{release();upstream.closeAllConnections();return new Promise(r=>upstream.close(r));});
 const host=await startHost({gatewayOrigin:`http://127.0.0.1:${upstream.address().port}`,hostSecret:'s'.repeat(43),now:()=>clock});t.after(()=>host.close());
 const call=async(path,{cookie,token,body}={})=>{
  const r=await fetch(host.origin+path,{method:body?'POST':'GET',headers:{...(body?{'Content-Type':'application/json',Origin:host.origin}:{}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:body?JSON.stringify(body):undefined});
  return {status:r.status,data:await r.json(),cookie:r.headers.get('set-cookie')?.split(';')[0]};
 };
 const login=await call('/demo/login',{body:{account:'lin'}}),cookie=login.cookie;
 const first=await call('/host/chat-session',{cookie,body:{}}),token=first.data.access_token;
 const flight=call('/chat/v1/messages',{cookie,token,body:{event_id:'original',content:'A private question'}});
 await waiting;
 let b;
 if(change==='expire')clock+=1800001;
 else b=await call('/demo/login',{cookie,body:{account:'zhou'}});
 release();const late=await flight;
 assert.equal(late.status,401);assert.equal(late.data.code,'chat_session_required');assert.doesNotMatch(JSON.stringify(late.data),/private-reply-A/);
 assert.equal((await call('/host/chat-session',{cookie,body:{}})).status,401);
 if(b){const bs=await call('/host/chat-session',{cookie:b.cookie,body:{}});assert.notEqual(bs.data.conversation_id,first.data.conversation_id);assert.deepEqual((await call('/chat/v1/messages',{cookie:b.cookie,token:bs.data.access_token})).data.messages,[]);}
 const again=await call('/demo/login',{body:{account:'lin'}}),session=await call('/host/chat-session',{cookie:again.cookie,body:{}});
 assert.equal(session.data.conversation_id,first.data.conversation_id);
 const history=await call('/chat/v1/messages',{cookie:again.cookie,token:session.data.access_token});assert.equal(history.data.messages.length,2);assert.equal(calls,1);
});
