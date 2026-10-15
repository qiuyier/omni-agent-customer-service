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
test('send loss truncates a successful body; identical retries keep auditable payload hashes and receipt IDs',async t=>{
 let calls=0;const receipts=[];
 const server=http.createServer((req,res)=>{req.resume();req.on('end',()=>{calls++;res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({message_id:'message-1',reply_message_id:'reply-1',content:'must-not-log'}));});});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));
 const gate=await startResponseGate('http://127.0.0.1:'+server.address().port,{observeReceipt:r=>receipts.push(r)});
 t.after(async()=>{await gate.close();server.closeAllConnections();await new Promise(r=>server.close(r));});
 gate.loseNextSend();assert.throws(()=>gate.loseNextSend());
 const send=()=>fetch(gate.origin+'/chat/v1/messages',{method:'POST',headers:{Origin:gate.origin,'Content-Type':'application/json'},body:JSON.stringify({event_id:'same-event',content:'test-only'})});
 await assert.rejects(async()=>{const response=await send();await response.json();});
 assert.equal(calls,1);assert.equal((await (await send()).json()).message_id,'message-1');assert.equal(calls,2);
 assert.equal(receipts.length,2);assert.equal(receipts[0].dropped,true);assert.equal(receipts[1].dropped,false);
 assert.equal(receipts[0].request_sha256,receipts[1].request_sha256);assert.equal(receipts[0].message_id,receipts[1].message_id);
 assert.ok(!JSON.stringify(receipts).includes('must-not-log'));assert.ok(!JSON.stringify(receipts).includes('test-only'));
});
