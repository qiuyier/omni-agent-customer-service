import test from 'node:test';
import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import {browserGate} from './browser-gate.mjs';
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
