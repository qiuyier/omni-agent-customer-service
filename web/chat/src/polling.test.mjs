import test from 'node:test';
import assert from 'node:assert/strict';
import {createPoller,readDeskSnapshot} from './polling.mjs';

function clock(read){
 let visible=true,listener;const timers=new Map(),states=[];let id=0;
 const p=createPoller({read,onState:s=>states.push(s),visible:()=>visible,subscribe:fn=>{listener=fn;return()=>listener=null;},schedule:(fn,delay)=>{timers.set(++id,{fn,delay});return id;},unschedule:key=>timers.delete(key)});
 return {p,states,timers,hide(){visible=false;listener?.();},show(){visible=true;listener?.();},delay:()=>[...timers.values()][0]?.delay,tick(){const [key,value]=timers.entries().next().value;timers.delete(key);return value.fn();}};
}
test('polls only after completion and never overlaps slow reads',async()=>{
 let resolve,calls=0;const c=clock(()=>{calls++;return new Promise(r=>resolve=r);});
 assert.equal(c.delay(),5000);const first=c.tick();assert.equal(calls,1);assert.equal(c.timers.size,0);
 c.show();assert.equal(c.timers.size,0);resolve();await first;assert.equal(c.timers.size,1);c.p.stop();assert.equal(c.timers.size,0);
});
test('hidden pages abort reads and reject late results, then resume',async()=>{
 let finish,applied=0,signal;const c=clock(async task=>{signal=task.signal;await new Promise(r=>finish=r);if(task.active())applied++;});
 const work=c.tick();c.hide();assert.equal(signal.aborted,true);finish();await work;assert.equal(applied,0);assert.equal(c.timers.size,0);assert.equal(c.states.at(-1),'paused');
 c.show();assert.equal(c.delay(),0);c.p.stop();
});
test('failures back off to 60 seconds and success resets delay',async()=>{
 let fail=true;const c=clock(async()=>{if(fail)throw Error('offline');});
 for(const delay of [10000,20000,40000,60000,60000]){await c.tick();assert.equal(c.delay(),delay);}
 assert.equal(c.states.at(-1),'retrying');fail=false;await c.tick();assert.equal(c.delay(),5000);assert.equal(c.states.at(-1),'active');c.p.stop();
});
test('identity/conversation invalidation and unmount suppress late completion',async()=>{
 for(const end of ['invalidate','stop']){let finish,applied=0;const c=clock(async task=>{await new Promise(r=>finish=r);if(task.active())applied++;});const work=c.tick();c.p[end]();finish();await work;assert.equal(applied,0);c.p.stop();}
});
test('staff poll reads queue and history only, never claim/reply/close',async()=>{
 const requests=[];const result=await readDeskSnapshot(async(path,opts)=>{requests.push({path,...opts});return path==='/desk/me'?{agent:{id:'qiao'}}:path==='/desk/queue'?{items:[]}:{conversation_id:'round-2',messages:[]};},'qiao','round-2');
 assert.equal(result.selected.conversation_id,'round-2');assert.deepEqual(requests.map(r=>r.path),['/desk/me','/desk/queue','/desk/action','/desk/me']);assert.deepEqual(requests[2].body,{conversation_id:'round-2',action:'history'});
});
test('staff identity change cannot query previous staff selection',async()=>{
 const paths=[];const data=await readDeskSnapshot(async path=>{paths.push(path);return {agent:{id:'wen'}};},'qiao','old-round');assert.equal(data.changed,true);assert.deepEqual(paths,['/desk/me']);
});
test('staff rejects a history response for a different conversation',async()=>{
 await assert.rejects(readDeskSnapshot(async path=>path==='/desk/me'?{agent:{id:'qiao'}}:path==='/desk/queue'?{items:[]}:{conversation_id:'wrong'},'qiao','expected'),/invalid_history/);
});
test('staff switch during reads discards the old snapshot',async()=>{
 let identityReads=0;const result=await readDeskSnapshot(async path=>path==='/desk/me'?{agent:{id:++identityReads===1?'qiao':'wen'}}:path==='/desk/queue'?{items:[]}:{conversation_id:'old',messages:[{content:'old-private'}]},'qiao','old');
 assert.equal(result.changed,true);assert.equal(result.me.agent.id,'wen');assert.equal(result.selected,undefined);
});
