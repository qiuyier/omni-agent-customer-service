import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,chmod,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import http from 'node:http';
import {aiEnvironment,loadAI} from './ai.mjs';
import {environment,newConfig} from './config.mjs';
import {startBusiness} from '../../integrations/fresh-onboarding/business.mjs';
import {attachSupport} from '../../integrations/fresh-onboarding/support.mjs';
import {supportProxyTimeout,validateSupportConfig} from '../../integrations/host-kit/onboarding.mjs';
const ai=()=>({provider:'custom',protocol:'chat_completions',model:'offline-model',baseURL:'https://model.invalid/v1',apiKey:'offline-only-secret',faqPath:'/tmp/fictional-faq.json'});
test('AI configuration explicit and bounded; normal environment remains fake',()=>{
 const cfg=ai(),env=aiEnvironment(cfg);assert.equal(env.WEBCHAT_REPLY_MODE,'ai');assert.equal(env.KNOWLEDGE_RETRIEVER,'keyword');assert.equal(env.AI_ALLOW_INSECURE_HTTP,'false');
 assert.equal(environment(newConfig()).WEBCHAT_REPLY_MODE,'fake');assert.equal(environment(newConfig()).AI_API_KEY,undefined);
 for(const change of [{apiKey:''},{faqPath:'relative.json'},{protocol:'responses'},{baseURL:'http://127.0.0.1:1234'},{baseURL:'https://user:pass@example.com'},{baseURL:'https://example.com/chat/completions'},{baseURL:'https://example.com/?key=private'},{model:'x\nsecret'},{unexpected:true}])assert.throws(()=>aiEnvironment({...cfg,...change}));
 assert.equal(supportProxyTimeout('ai'),125000);assert.equal(supportProxyTimeout('fake'),20000);assert.throws(()=>supportProxyTimeout('unknown'));
 const base={gatewayOrigin:'http://127.0.0.1:8095',hostSecret:'h'.repeat(43),agents:[{id:'staff',name:'Staff',secret:'a'.repeat(43)}],replyMode:'ai',storage:'postgres'};
 assert.equal(validateSupportConfig(base).replyMode,'ai');assert.throws(()=>validateSupportConfig({...base,storage:'memory'}));
});
test('AI file missing, malformed or public permissions fails closed',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'omni-reference-ai-'));t.after(()=>rm(dir,{recursive:true,force:true}));const file=join(dir,'ai.json');
 await assert.rejects(loadAI(dir));await writeFile(file,'{',{mode:0o600});await assert.rejects(loadAI(dir));
 await writeFile(file,JSON.stringify(ai()));assert.equal((await loadAI(dir)).AI_MODEL,'offline-model');await chmod(file,0o644);await assert.rejects(loadAI(dir));
});
test('real business and shared adapter propagate AI mode; upstream failure is not fake success or retry',async t=>{
 let sends=0;const c=newConfig();
 const gateway=http.createServer(async(req,res)=>{
  for await(const part of req){} res.setHeader('Content-Type','application/json');
  if(req.url==='/internal/chat/sessions'){res.writeHead(201).end(JSON.stringify({access_token:'chat_'+'t'.repeat(43),expires_at:new Date(Date.now()+300000).toISOString(),conversation_id:'wbc_'+'a'.repeat(64)}));return;}
  sends++;res.writeHead(503).end(JSON.stringify({error:'PRIVATE_UPSTREAM_DETAIL'}));
 });
 await new Promise(r=>gateway.listen(0,'127.0.0.1',r));t.after(()=>{gateway.closeAllConnections();return new Promise(r=>gateway.close(r));});
 const business=await startBusiness({accounts:c.accounts});t.after(()=>business.close());
 const support=await attachSupport(business,{gatewayOrigin:'http://127.0.0.1:'+gateway.address().port,hostSecret:c.hostSecret,agents:[{id:'staff-1',name:'Staff',secret:c.agentSecret}],storage:'postgres',replyMode:'ai'});t.after(()=>support.close());
 let cookie;const call=async(path,body,token)=>{const r=await fetch(business.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:business.origin,...(body===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(5000)});return {status:r.status,cookie:r.headers.get('set-cookie')?.split(';')[0],data:await r.json()};};
 const login=await call('/auth/customer/login',{account:c.accounts[0].handle,password:c.accounts[0].passphrase});assert.equal(login.status,200);cookie=login.cookie;
 const me=await call('/host/me');assert.equal(me.data.reply_mode,'ai');assert.equal(me.data.human_enabled,true);
 const session=await call('/host/chat-session',{});assert.equal(session.status,200);assert.equal(session.data.reply_mode,'ai');
 const result=await call('/chat/v1/messages',{event_id:'offline-ai-failure',content:'Fictional question'},session.data.access_token);
 assert.equal(result.status,503);assert.equal(sends,1);assert.ok(!JSON.stringify(result).includes('PRIVATE_UPSTREAM_DETAIL'));assert.equal(result.data.reply_message_id,undefined);
});
