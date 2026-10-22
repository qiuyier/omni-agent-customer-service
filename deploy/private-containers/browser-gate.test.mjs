import test from 'node:test';
import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import {browserGate,responseLoss} from './browser-gate.mjs';
test('only selected chat operations are held; release permits forwarding',async()=>{
 let mode='renewal';const log=[];const res=new EventEmitter();
 const gate=browserGate({readMode:()=>mode,log:x=>log.push(x)});
 assert.equal(await gate({url:'/auth/customer/logout',method:'POST'},res),true);
 const held=gate({url:'/host/chat-session',method:'POST'},res);mode='';
 assert.equal(await held,true);assert.deepEqual(log.map(x=>x.gate),['held','released']);
 assert.equal(res.listenerCount('close'),0);
});
test('closed browser cancels held request rather than forwarding after release',async()=>{
 const res=new EventEmitter();const gate=browserGate({readMode:()=> 'send'});
 assert.equal(await gate({url:'/chat/v1/messages',method:'GET'},res),true);
 const held=gate({url:'/chat/v1/messages',method:'POST'},res);res.emit('close');
 assert.equal(await held,false);assert.equal(res.listenerCount('close'),0);
});
test('hold deadline fails closed',async()=>{
 const res=new EventEmitter();const gate=browserGate({readMode:()=> 'renewal',timeout:5});
 assert.equal(await gate({url:'/host/chat-session',method:'POST'},res),false);
});

test('response loss targets only completed successful sends, not receipt or auth',()=>{
 let mode='drop';const logs=[];const loss=responseLoss({readMode:()=>mode,log:e=>logs.push(e)});
 assert.equal(loss({url:'/chat/v1/messages',method:'POST'},200),false);
 for(const [url,method,status] of [['/chat/v1/messages','POST',401],['/chat/v1/messages','GET',200],['/chat/v1/message-receipt','POST',200],['/auth/customer/login','POST',200]])assert.equal(loss({url,method},status),true);
 mode='';assert.equal(loss({url:'/chat/v1/messages',method:'POST'},200),true);assert.equal(logs.length,1);
});
test('receipt outage fails closed without affecting identity or history',async()=>{
 let mode='drop-block';const gate=browserGate({readMode:()=>mode});const res=new EventEmitter();
 assert.equal(await gate({url:'/chat/v1/message-receipt',method:'POST'},res),false);
 assert.equal(await gate({url:'/auth/customer/me',method:'GET'},res),true);
 assert.equal(await gate({url:'/chat/v1/messages',method:'GET'},res),true);
 mode='';assert.equal(await gate({url:'/chat/v1/message-receipt',method:'POST'},res),true);
});
