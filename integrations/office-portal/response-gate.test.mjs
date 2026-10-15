import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {startResponseGate} from './response-gate.mjs';
async function fixture(t){
 const seen=[],events=[];let captured;
 const capture=new Promise(resolve=>captured=resolve);
 const upstream=http.createServer((req,res)=>{seen.push({path:req.url,origin:req.headers.origin,host:req.headers.host});req.resume();res.writeHead(200,{'Content-Type':'application/json','Cache-Control':'no-store'});res.end(JSON.stringify({marker:'old-account-only'}));});
 await new Promise(r=>upstream.listen(0,'127.0.0.1',r));
 const target='http://127.0.0.1:'+upstream.address().port;
 const gate=await startResponseGate(target,{notify:event=>{events.push(event);if(event==='captured')captured();}});
 t.after(async()=>{await gate.close();upstream.closeAllConnections();await new Promise(r=>upstream.close(r));});
 return {gate,seen,events,capture,target};
}
test('test gate rejects non-loopback targets',async()=>{await assert.rejects(startResponseGate('https://example.com'));});
test('holds exactly one completed history response while other routes and auth remain live',async t=>{
 const {gate,seen,events,capture,target}=await fixture(t);gate.arm();let done=false;
 const pending=fetch(gate.origin+'/chat/v1/messages',{headers:{Origin:gate.origin}}).then(r=>r.json()).then(r=>{done=true;return r;});
 await capture;assert.equal(done,false);assert.deepEqual(gate.status(),{armed:false,held:true});
 assert.equal((await fetch(gate.origin+'/support/identity/member')).status,200);
 assert.equal((await fetch(gate.origin+'/chat/v1/messages')).status,200);
 assert.throws(()=>gate.arm());
 assert.equal(gate.release(),true);assert.equal((await pending).marker,'old-account-only');
 assert.equal(seen[0].origin,target);assert.equal(seen[0].host,new URL(target).host);
 assert.deepEqual(events,['armed','captured','released']);assert.ok(!JSON.stringify(events).includes('old-account-only'));
});
test('foreign Origin and Host are rejected before upstream forwarding',async t=>{
 const {gate,seen}=await fixture(t);
 assert.equal((await fetch(gate.origin+'/chat/v1/messages',{headers:{Origin:'http://elsewhere.invalid'}})).status,403);
 const status=await new Promise((resolve,reject)=>{const req=http.get(gate.origin,{headers:{Host:'elsewhere.invalid'}},res=>{res.resume();resolve(res.statusCode);});req.on('error',reject);});
 assert.equal(status,403);assert.equal(seen.length,0);
});
test('closing gate destroys a held request and clears its payload',async t=>{
 const {gate,capture}=await fixture(t);gate.arm();
 const pending=fetch(gate.origin+'/chat/v1/messages').catch(()=>null);
 await capture;await gate.close();assert.equal(await pending,null);assert.deepEqual(gate.status(),{armed:false,held:false});
});
