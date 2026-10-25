// Explicit local acceptance client; never starts/stops services or migrates data.
import {readFile,writeFile} from 'node:fs/promises';
import {randomUUID} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import assert from 'node:assert/strict';
import {load,ports} from './config.mjs';
import {acceptHost} from '../../integrations/host-kit/acceptance.mjs';
const directory=fileURLToPath(new URL('../../.local/reference/',import.meta.url));
const origin=`http://127.0.0.1:${ports.business}`;
const report={passed:false,external_model_calls:0,checks:[]};let cookie;
async function call(path,body,token){
 const r=await fetch(origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:origin,...(body===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(15000)});
 const raw=await r.text();return {status:r.status,data:raw?JSON.parse(raw):null,cookie:r.headers.get('set-cookie')?.split(';')[0]};
}
try{
 const mode=process.argv[2];assert.equal(process.argv.length,3);assert.ok(['--allow-test-writes','--verify-restart'].includes(mode));
 const c=await load(directory);const credentials=a=>({account:a.handle,password:a.passphrase});
 if(mode==='--allow-test-writes'){
  const input=JSON.parse(await readFile(new URL('../../integrations/host-kit/starter/acceptance.example.json',import.meta.url),'utf8'));
  input.preflight.origin=origin;input.lifecycle.customers=c.accounts.slice(0,2).map(credentials);input.lifecycle.staff=credentials(c.accounts[2]);
  const result=await acceptHost(input,{allowTestWrites:true});assert.equal(result.phases.lifecycle?.passed,true);report.checks.push('shared_lifecycle_passed');
 }
 const login=await call('/auth/customer/login',credentials(c.accounts[0]));assert.equal(login.status,200);assert.ok(login.cookie);cookie=login.cookie;
 const session=await call('/host/chat-session',{});assert.equal(session.status,200);assert.equal(session.data.storage,'postgres');assert.equal(session.data.reply_mode,'fake');
 const token=session.data.access_token;
 if(mode==='--allow-test-writes'){
  const event=randomUUID(),content='REFERENCE restart proof '+event;
  const sent=await call('/chat/v1/messages',{event_id:event,content},token);assert.equal(sent.status,200);
  const history=await call('/chat/v1/messages',undefined,token);assert.equal(history.status,200);
  await writeFile(directory+'restart-proof.json',JSON.stringify({conversation:session.data.conversation_id,event,content,history:history.data},null,2)+'\n',{mode:0o600});report.checks.push('restart_snapshot_saved');
 }else{
  const saved=JSON.parse(await readFile(directory+'restart-proof.json','utf8'));
  assert.equal(session.data.conversation_id,saved.conversation);
  const history=await call('/chat/v1/messages',undefined,token);assert.equal(history.status,200);assert.deepEqual(history.data,saved.history);
  assert.equal(history.data.messages.filter(m=>m.content===saved.content).length,1);report.checks.push('same_conversation_and_exact_history_after_restart');
 }
 report.passed=true;
}catch{report.error='reference_acceptance_failed';process.exitCode=1;}
finally{
 if(cookie){try{assert.equal((await call('/auth/customer/logout',{})).status,204);}catch{report.passed=false;report.error='logout_failed';process.exitCode=1;}}
 console.log(JSON.stringify(report));
}
