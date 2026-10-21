import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {mountSupport, validateSupportConfig} from './onboarding.mjs';
const config=()=>({gatewayOrigin:'http://127.0.0.1:1',hostSecret:'h'.repeat(43),agents:[{id:'staff',name:'Staff',secret:'a'.repeat(43)}],storage:'memory',replyMode:'fake'});
test('explicit local fake configuration and independent authorized agents required',()=>{
  assert.equal(validateSupportConfig(config()).replyMode,'fake');
  for(const change of [{storage:undefined},{replyMode:'ai'},{agents:[]},{gatewayOrigin:'http://localhost:8080'},{gatewayOrigin:'http://127.0.0.1:1/'},{hostSecret:'short'}])assert.throws(()=>validateSupportConfig({...config(),...change}));
  const c=config();c.agents[0].secret=c.hostSecret;assert.throws(()=>validateSupportConfig(c));
});
test('missing identity or mount is rejected before mounting',async()=>{
  let mounted=false;
  await assert.rejects(mountSupport({config:config(),publicOrigin:()=> 'http://127.0.0.1:1',mount(){mounted=true;}}));assert.equal(mounted,false);
});
test('mount failure is sanitized and releases its private listener',async()=>{
  // This promise resolves only after adapter.close completed; no business service is closed.
  await assert.rejects(mountSupport({config:config(),sessions:{authenticate:()=>null},publicOrigin:()=> 'http://127.0.0.1:1',mount(){throw Error('PRIVATE_MOUNT_SECRET');}}),/^Error: support_mount_failed$/);
});
test('real shared proxy keeps business routes and rejects revoked identity; close is idempotent',async t=>{
  let handler,active=true,origin;
  const server=http.createServer(async(req,res)=>{
    if(handler&&await handler(req,res))return;
    res.writeHead(200,{'Content-Type':'application/json'}).end('{"business":true}');
  });
  await new Promise(r=>server.listen(0,'127.0.0.1',r));origin='http://127.0.0.1:'+server.address().port;
  t.after(()=>{server.closeAllConnections();return new Promise(r=>server.close(r));});
  const sessions={authenticate(req,role){return active&&role==='customer'&&req.headers.cookie==='session=verified'?{role,enabled:true,subjectID:'member-a',sessionID:'opaque-session-123456789',expiresAt:Date.now()+60000,version:'public-version-123456'}:null;}};
  const support=await mountSupport({config:config(),sessions,publicOrigin:()=>origin,mount(h){handler=h;}});t.after(()=>support.close());
  const call=async path=>{const r=await fetch(origin+path,{headers:{Cookie:'session=verified'},signal:AbortSignal.timeout(3000)});return {status:r.status,data:await r.json()};};
  assert.equal((await call('/host/me')).data.user.id,'member-a');
  assert.equal((await call('/business')).data.business,true);
  active=false;assert.equal((await call('/host/chat-session')).status,405);
  const rejected=await fetch(origin+'/host/chat-session',{method:'POST',headers:{Origin:origin,'Content-Type':'application/json',Cookie:'session=verified'},body:'{}'});assert.equal(rejected.status,401);await rejected.body.cancel();
  const one=support.close(),two=support.close();assert.equal(one,two);await one;
  assert.equal((await call('/business')).data.business,true);
});
