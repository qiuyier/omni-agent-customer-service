#!/usr/bin/env node
// Owns all resources: no existing DSN, account file, model endpoint or volume.
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import {createServer} from 'node:http';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomUUID,randomBytes} from 'node:crypto';
import assert from 'node:assert/strict';
import {cleanEnvironment} from './recovery_check_support.mjs';
import {AdminController,APIError} from '../web/chat/src/admin.mjs';
const args=process.argv.slice(2);
if(args.length===1&&args[0]==='--help'){console.log('node scripts/knowledge_feedback_flow_check.mjs --allow-test-writes\nCreates and removes a dedicated PostgreSQL container/volume. Local mock model only; existing deployment settings are not inherited.');process.exit(0);}
if(args.length!==1||args[0]!=='--allow-test-writes'){console.error('Explicit --allow-test-writes required');process.exit(2);}
const root=fileURLToPath(new URL('../',import.meta.url)),output=await fs.mkdtemp(join(tmpdir(),'omni-feedback-flow-'));
const env=cleanEnvironment(process.env),id=randomUUID(),container='omni-feedback-'+id,volume=container+'-data',label='omni.feedback-flow='+id;
const report={status:'running',external_models:false,scope:'real HTTP processes + frontend controller + PostgreSQL; not browser rendering or model answer quality',checks:[],cleanup:[]};
const children=new Set();let gateway,admin,created=false,volumeCreated=false,interrupted=false,mock;
const save=()=>fs.writeFile(join(output,'report.json'),JSON.stringify(report,null,2)+'\n');
const mark=async name=>{report.checks.push({name,status:'passed'});await save();console.log('PASS '+name);};
function child(command,args,extra={},input){
 const p=spawn(command,args,{cwd:root,env:{...env,...extra},stdio:['pipe','pipe','pipe']});children.add(p);
 let out='';p.stdout.on('data',b=>{out+=b});p.stderr.on('data',()=>{});p.stdin.end(input);
 p.done=new Promise(resolve=>{p.on('error',()=>resolve({code:1,out:''}));p.on('close',code=>{children.delete(p);resolve({code,out});});});return p;
}
async function run(command,args,extra={},input){if(interrupted&&!cleaning)throw Error('interrupted');const p=child(command,args,extra,input),timer=setTimeout(()=>p.kill('SIGKILL'),180000);try{const r=await p.done;if(r.code!==0)throw Error('command failed: '+command);return r.out.trim();}finally{clearTimeout(timer);}}
async function stop(p){if(!p||p.exitCode!==null||p.signalCode)return;p.kill('SIGTERM');const timer=setTimeout(()=>p.kill('SIGKILL'),6000);await p.done;clearTimeout(timer);}
const docker=(...a)=>run('docker',['--context','desktop-linux',...a]);
async function wait(fn){for(let i=0;i<120;i++){if(interrupted)throw Error('interrupted');try{if(await fn())return;}catch{}await new Promise(r=>setTimeout(r,250));}throw Error('readiness timeout');}
async function port(){const s=createServer();await new Promise((r,j)=>{s.once('error',j);s.listen(0,'127.0.0.1',r)});const n=s.address().port;await new Promise(r=>s.close(r));return n;}
async function http(origin,path,body,headers={},status=200){const r=await fetch(origin+path,{method:body===undefined?'GET':'POST',headers:{...headers,...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(15000),redirect:'error'});assert.equal(r.status,status,path);return r;}
const json=async(...a)=>(await http(...a)).json(),bearer=token=>({Authorization:'Bearer '+token});
function statuses(out){
 const rows=out.split('\n').filter(l=>l.startsWith('{')).map(l=>JSON.parse(l)).filter(s=>s.event==='knowledge_feedback_status');
 assert.ok(rows.length>0,'missing feedback status');
 const allowed=['event','capture','at','recorded','failed','full','skipped','cleanup','cleanup_at','queue','active','expired','capacity'];
 for(const row of rows){assert.deepEqual(Object.keys(row).sort(),allowed.toSorted());assert.equal(row.capacity,500)}
 for(const text of ['fictional@example.org','How to change notifications?','fictional-only','postgres://'])assert.ok(!out.includes(text),'sensitive process output');
 return rows;
}
let cleaning=false;process.on('SIGINT',()=>{interrupted=true;for(const p of children)p.kill('SIGTERM')});process.on('SIGTERM',()=>{interrupted=true;for(const p of children)p.kill('SIGTERM')});
console.log('Report directory: '+output);
try{
 report.head=await run('git',['rev-parse','HEAD']);report.dirty=!!(await run('git',['status','--porcelain']));
 for(const name of ['migrate','webchat-server','knowledge-admin'])await run('go',['build','-o',join(output,name),'./cmd/'+name]);
 await docker('info','--format','{{.ServerVersion}}');volumeCreated=true;await docker('volume','create','--label',label,volume);
 const password=randomBytes(24).toString('hex');created=true;
 await docker('run','-d','--name',container,'--label',label,'-e','POSTGRES_USER=feedback_test','-e','POSTGRES_PASSWORD='+password,'-e','POSTGRES_DB=omni_feedback_test','-p','127.0.0.1::5432','-v',volume+':/var/lib/postgresql/data','postgres:16.15-bookworm');
 const ready=()=>wait(async()=>{await docker('exec',container,'pg_isready','-U','feedback_test','-d','omni_feedback_test');return true});await ready();
 const dsn=async()=>{const mapping=await docker('port',container,'5432/tcp');assert.match(mapping,/^127\.0\.0\.1:\d+$/);return 'postgres://feedback_test:'+password+'@'+mapping+'/omni_feedback_test?sslmode=disable'};
 let database=await dsn();
 const sql=q=>docker('exec',container,'psql','-X','-q','-t','-A','-v','ON_ERROR_STOP=1','-U','feedback_test','-d','omni_feedback_test','-c',q);
 await run(join(output,'migrate'),[],{DATABASE_URL:database});assert.equal(await sql('SELECT version FROM public.customer_service_schema_version'),'4');
 const requests=[];let mockFailure=false;
 mock=createServer(async(req,res)=>{try{
  assert.equal(req.method,'POST');assert.equal(req.url,'/v1/chat/completions');assert.equal(req.headers.authorization,'Bearer fictional-only');
  let raw='';for await(const b of req){raw+=b;if(raw.length>200000)throw Error('large mock request')}
  const body=JSON.parse(raw);assert.equal(body.model,'mock');assert.ok(Array.isArray(body.messages));requests.push(body);
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({id:'fixture',object:'chat.completion',created:1,model:'mock',choices:[{index:0,message:{role:'assistant',content:'Fictional local reply only.'},finish_reason:'stop'}],usage:{prompt_tokens:1,completion_tokens:1,total_tokens:2}}));
 }catch{mockFailure=true;res.statusCode=500;res.end('mock contract failure')}});
 await new Promise((r,j)=>{mock.once('error',j);mock.listen(0,'127.0.0.1',r)});
 const modelURL='http://127.0.0.1:'+mock.address().port+'/v1';
 const adminPort=await port(),adminOrigin='http://127.0.0.1:'+adminPort,accountFile=join(output,'accounts.json'),adminPassword='fictional-feedback-admin-only';
 const hash=await run(join(output,'knowledge-admin'),['-hash-password'],{},adminPassword+'\n');
 await fs.writeFile(accountFile,JSON.stringify([{id:'fixture',password_hash:hash,permissions:['knowledge.read','knowledge.edit','knowledge.publish']},{id:'reader',password_hash:hash,permissions:['knowledge.read']}]),{mode:0o600});
 const startAdmin=async()=>{admin=child(join(output,'knowledge-admin'),[],{DATABASE_URL:database,KNOWLEDGE_ADMIN_ADDRESS:'127.0.0.1:'+adminPort,KNOWLEDGE_ADMIN_ORIGIN:adminOrigin,KNOWLEDGE_ADMIN_ACCOUNTS_FILE:accountFile});await wait(async()=>admin.exitCode===null&&(await fetch(adminOrigin+'/',{signal:AbortSignal.timeout(1000)})).status===200)};
 await startAdmin();let cookie;
 const login=async account=>{const r=await http(adminOrigin,'/auth/login',{account,password:adminPassword},{Origin:adminOrigin});cookie=r.headers.get('set-cookie').split(';')[0]};await login('fixture');
 const manage=(path,body,status=200)=>json(adminOrigin,path,body,{Origin:adminOrigin,Cookie:cookie},status);
 const control=new AdminController(async(path,body,version)=>{const r=await fetch(adminOrigin+path,{method:body===undefined?'GET':'POST',headers:{Origin:adminOrigin,Cookie:cookie,...(version?{'X-Knowledge-Session':version}:{}),...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(15000),redirect:'error'});const data=await r.json();if(!r.ok)throw new APIError(r.status,data.code);return data});
 await manage('/internal/knowledge/draft',{revision:0,entries:[]});await manage('/internal/knowledge/publish',{revision:1,allow_empty:true});
 const gatewayPort=await port(),origin='http://127.0.0.1:'+gatewayPort,secret=randomBytes(32).toString('hex');
 const startGateway=async capture=>{gateway=child(join(output,'webchat-server'),[],{DATABASE_URL:database,WEBCHAT_ADDRESS:'127.0.0.1:'+gatewayPort,WEBCHAT_HOST_ID:'feedback-fixture',WEBCHAT_HOST_SECRET:secret,WEBCHAT_REPLY_MODE:'ai',WEBCHAT_KNOWLEDGE_SOURCE:'managed',WEBCHAT_KNOWLEDGE_FEEDBACK:String(capture),KNOWLEDGE_RETRIEVER:'keyword',KNOWLEDGE_RETRIEVAL_TIMEOUT:'2s',AI_PROVIDER:'custom',AI_PROTOCOL:'chat_completions',AI_MODEL:'mock',AI_BASE_URL:modelURL,AI_API_KEY:'fictional-only',AI_ALLOW_INSECURE_HTTP:'true'});await wait(async()=>gateway.exitCode===null&&(await fetch(origin+'/readyz',{signal:AbortSignal.timeout(1000)})).status===200)};
 const mint=()=>json(origin,'/internal/chat/sessions',{customer_id:'fictional-customer'},bearer(secret),201);
 const send=(s,event,content)=>json(origin,'/chat/v1/messages',{event_id:event,content},bearer(s.access_token));
 const list=()=>manage('/internal/knowledge/feedback');
 await startGateway(false);let session=await mint();const question='How to change notifications? contact fictional@example.org';
 await send(session,'capture-off',question);assert.equal((await list()).items.length,0);assert.equal(requests.length,1);await mark('capture_off_still_replies_without_feedback');
 await stop(gateway);const offStatus=statuses((await gateway.done).out).at(-1);assert.equal(offStatus.capture,'disabled');assert.equal(offStatus.recorded,0);assert.equal(offStatus.cleanup,'ok');await mark('disabled_runtime_status_is_private_and_cleanup_runs');
 await startGateway(true);session=await mint();assert.equal((await list()).items.length,0);assert.equal(requests.length,1);
 await send(session,'capture-on',question);let items=(await list()).items;assert.equal(items.length,1);assert.equal(items[0].question,'How to change notifications? contact [已脱敏]');assert.equal(items[0].status,'pending');assert.equal(items[0].count,1);
 assert.equal(await sql("SELECT count(*) FROM customer_service.knowledge_feedback WHERE question LIKE '%fictional@example.org%'"),'0');await mark('miss_captured_redacted_without_readiness_side_effect');
 await send(session,'capture-on',question);assert.equal((await list()).items[0].count,1);assert.equal(requests.length,2);await mark('duplicate_message_does_not_duplicate_capture_or_model_call');
 await control.open();await control.loadFeedback();const item=control.state.feedback[0];assert.ok(control.feedbackToDraft(item));assert.equal(control.state.dirty,true);assert.equal(control.state.entries[0].content,'');assert.equal((await manage('/internal/knowledge/state')).revision,2);
 const faqID=control.state.entries[0].id,answer='FICTIONAL_NOTIFICATION_GUIDE_42: open preferences and choose notifications.';
 control.edit(control.state.entries.map(e=>({...e,content:answer,source:'fictional acceptance guide',keywords:['notifications']})));await control.mutate('draft');assert.equal(control.state.data.revision,3);assert.equal(control.state.dirty,false);
 await send(session,'draft-only',question);assert.ok(!JSON.stringify(requests.at(-1)).includes(answer));assert.equal((await list()).items[0].status,'pending');await mark('local_draft_and_saved_draft_do_not_change_published_answers');
 const beforePublishCalls=requests.length;await control.mutate('publish');assert.equal(control.state.data.revision,4);await control.loadFeedback();await control.checkFeedback(control.state.feedback[0],faqID);assert.equal(control.state.feedbackCheck?.outcome,'matched');assert.equal(requests.length,beforePublishCalls);assert.equal((await list()).items[0].status,'pending');await mark('publish_then_verify_matches_without_model_or_auto_resolution');
 const change={id:item.id,revision:item.revision,status:'resolved',faq_id:faqID};await control.loadFeedback(change);assert.equal(control.state.feedback[0].status,'resolved');assert.equal(control.state.feedback[0].faq_id,faqID);await mark('explicit_manual_resolution_persisted');
 await manage('/internal/knowledge/feedback/status',change,409);
 await manage('/internal/knowledge/feedback/check',{id:item.id,item_revision:1,revision:4,expected_id:faqID},409);
 await manage('/internal/knowledge/feedback/check',{id:item.id,item_revision:2,revision:3,expected_id:faqID},409);
 const adminCookie=cookie;await login('reader');await manage('/internal/knowledge/feedback/status',{...change,revision:2,status:'ignored',faq_id:''},403);await manage('/internal/knowledge/publish',{revision:4},403);cookie=adminCookie;await mark('stale_versions_and_insufficient_permissions_rejected');
 await send(session,'after-publish',question);assert.ok(JSON.stringify(requests.at(-1)).includes(answer));const savedItems=(await list()).items,savedKnowledge=await manage('/internal/knowledge/state');assert.equal(savedItems[0].count,2);await mark('subsequent_customer_request_receives_new_knowledge_context');
 const history=await json(origin,'/chat/v1/messages',undefined,bearer(session.access_token));
 await stop(gateway);const enabledStatus=statuses((await gateway.done).out).at(-1);assert.equal(enabledStatus.capture,'enabled');assert.equal(enabledStatus.recorded,2);assert.equal(enabledStatus.failed,0);assert.equal(enabledStatus.full,0);assert.equal(enabledStatus.active,1);assert.equal(enabledStatus.cleanup,'ok');
 await stop(admin);assert.equal(statuses((await admin.done).out).at(-1).capture,'not_owned');await mark('enabled_counters_queue_and_admin_scope_reported_without_text');
 await docker('restart',container);await ready();database=await dsn();await startAdmin();await startGateway(true);
 await http(adminOrigin,'/auth/me',undefined,{Cookie:cookie},401);await login('fixture');assert.deepEqual((await list()).items,savedItems);assert.deepEqual(await manage('/internal/knowledge/state'),savedKnowledge);
 const restored=await mint();assert.equal(restored.conversation_id,session.conversation_id);assert.deepEqual(await json(origin,'/chat/v1/messages',undefined,bearer(restored.access_token)),history);
 await send(restored,'after-restart',question);assert.ok(JSON.stringify(requests.at(-1)).includes(answer));assert.deepEqual((await list()).items,savedItems);await mark('database_and_process_restart_preserves_state_and_retrieval');
 // Expiration is simulated only inside this script-owned disposable database.
 await sql("UPDATE customer_service.knowledge_feedback SET first_seen=first_seen-interval '8 days',expires_at=expires_at-interval '8 days'");assert.equal((await list()).items.length,0);
 await manage('/internal/knowledge/feedback/check',{id:item.id,item_revision:2,revision:4,expected_id:faqID},404);await manage('/internal/knowledge/feedback/status',{...change,revision:2},409);await mark('expired_questions_hidden_and_actions_rejected');
 await stop(gateway);await startGateway(false);await wait(async()=>(await sql('SELECT count(*) FROM customer_service.knowledge_feedback'))==='0');await mark('startup_cleanup_removes_expired_text_even_with_capture_disabled');
 await stop(gateway);const finalStatus=statuses((await gateway.done).out).at(-1);assert.equal(finalStatus.capture,'disabled');assert.equal(finalStatus.active,0);assert.equal(finalStatus.expired,0);assert.equal(finalStatus.cleanup,'ok');assert.equal(finalStatus.recorded,0);await mark('disabled_restart_status_confirms_cleanup_and_counter_reset');
 assert.equal(mockFailure,false);assert.equal(requests.length,5);report.local_model_requests=requests.length;report.status='passed';
}catch(e){report.status='failed';report.failure=e.message?.replace(/postgres:\/\/[^\s]+/g,'[redacted]');process.exitCode=1;}
finally{
 cleaning=true;for(const p of [...children])await stop(p);if(mock){mock.closeAllConnections();await new Promise(r=>mock.close(r));}
 for(const [kind,name,present] of [['container',container,created],['volume',volume,volumeCreated]])if(present){try{const inspect=kind==='container'?['inspect']:['volume','inspect'];const format=kind==='container'?'{{index .Config.Labels "omni.feedback-flow"}}':'{{index .Labels "omni.feedback-flow"}}';assert.equal(await docker(...inspect,'--format',format,name),id);await docker(...(kind==='container'?['rm','-f',name]:['volume','rm',name]));report.cleanup.push({kind,status:'removed'});}catch{report.cleanup.push({kind,status:'failed'});report.status='failed';process.exitCode=1;}}
 await fs.rm(join(output,'accounts.json'),{force:true});await save();console.log(report.status.toUpperCase()+' '+join(output,'report.json'));
}
