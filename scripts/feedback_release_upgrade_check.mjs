#!/usr/bin/env node
// Disposable v1.1.0 -> current schema upgrade. No existing DSN or real models.
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import {createServer} from 'node:net';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomUUID,randomBytes} from 'node:crypto';
import assert from 'node:assert/strict';
import {cleanEnvironment} from './recovery_check_support.mjs';
const args=process.argv.slice(2);
if(args.length===1&&args[0]==='--help'){console.log('node scripts/feedback_release_upgrade_check.mjs --allow-test-writes\nCreates and removes a dedicated PostgreSQL container/volume; builds local v1.1.0 and current source; fake replies only.');process.exit(0);}
if(args.length!==1||args[0]!=='--allow-test-writes'){console.error('Explicit --allow-test-writes required');process.exit(2);}
const root=fileURLToPath(new URL('../',import.meta.url)),output=await fs.mkdtemp(join(tmpdir(),'omni-feedback-upgrade-'));
const env=cleanEnvironment(process.env),id=randomUUID(),container='omni-feedback-upgrade-'+id,volume=container+'-data',label='omni.feedback-upgrade='+id;
const report={status:'running',external_models:false,checks:[],cleanup:[]},children=new Set();
let gateway,admin,created=false,volumeCreated=false,interrupted=false;
const save=()=>fs.writeFile(join(output,'report.json'),JSON.stringify(report,null,2));
const mark=async name=>{report.checks.push({name,status:'passed'});await save();console.log('PASS '+name);};
function child(command,args,cwd=root,extra={},input){
 const p=spawn(command,args,{cwd,env:{...env,...extra},stdio:['pipe','pipe','pipe']});children.add(p);
 let out='',error='';p.stdout.on('data',b=>out+=b);p.stderr.on('data',b=>error+=b);p.stdin.end(input);
 p.done=new Promise(resolve=>{p.on('error',()=>resolve({code:1,out,error:'spawn failed'}));p.on('close',code=>{children.delete(p);resolve({code,out,error});});});return p;
}
async function run(command,args,cwd=root,extra={},input){const p=child(command,args,cwd,extra,input),timer=setTimeout(()=>p.kill('SIGKILL'),180000);try{const r=await p.done;if(r.code!==0)throw Error('command failed: '+command);return r.out.trim();}finally{clearTimeout(timer);}}
async function stop(p){if(!p||p.exitCode!==null||p.signalCode)return;p.kill('SIGTERM');const timer=setTimeout(()=>p.kill('SIGKILL'),6000);await p.done;clearTimeout(timer);}
const docker=(...args)=>run('docker',['--context','desktop-linux',...args]);
async function wait(fn){for(let i=0;i<120;i++){if(interrupted)throw Error('interrupted');try{if(await fn())return;}catch{}await new Promise(r=>setTimeout(r,250));}throw Error('readiness timeout');}
async function port(){const s=createServer();await new Promise((r,j)=>{s.once('error',j);s.listen(0,'127.0.0.1',r);});const p=s.address().port;await new Promise(r=>s.close(r));return p;}
async function http(origin,path,body,headers={},status=200){const r=await fetch(origin+path,{method:body===undefined?'GET':'POST',headers:{...headers,...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(10000),redirect:'error'});assert.equal(r.status,status,path);return r;}
async function json(origin,path,body,headers={},status=200){return (await http(origin,path,body,headers,status)).json();}
const bearer=token=>({Authorization:'Bearer '+token});
process.on('SIGINT',()=>{interrupted=true;});process.on('SIGTERM',()=>{interrupted=true;});
console.log('Report directory: '+output);
try{
 report.old_commit=await run('git',['rev-parse','v1.1.0^{commit}']);report.new_commit=await run('git',['rev-parse','HEAD']);report.dirty=!!(await run('git',['status','--porcelain']));
 const old=join(output,'old');await fs.mkdir(old);await run('git',['archive','--format=tar','--output='+join(output,'old.tar'),'v1.1.0']);await run('tar',['-xf',join(output,'old.tar'),'-C',old]);
 for(const name of ['migrate','webchat-server','knowledge-admin'])await run('go',['build','-o',join(output,'old-'+name),'./cmd/'+name],old);
 for(const name of ['migrate','webchat-server','knowledge-admin'])await run('go',['build','-o',join(output,'new-'+name),'./cmd/'+name]);
 await docker('info','--format','{{.ServerVersion}}');volumeCreated=true;await docker('volume','create','--label',label,volume);
 const password=randomBytes(24).toString('hex');created=true;
 await docker('run','-d','--name',container,'--label',label,'-e','POSTGRES_USER=upgrade_test','-e','POSTGRES_PASSWORD='+password,'-e','POSTGRES_DB=omni_upgrade_test','-p','127.0.0.1::5432','-v',volume+':/var/lib/postgresql/data','postgres:16.15-bookworm');
 const ready=()=>wait(async()=>{await docker('exec',container,'pg_isready','-U','upgrade_test','-d','omni_upgrade_test');return true;});await ready();
 const dsn=async()=>{const mapping=await docker('port',container,'5432/tcp');assert.match(mapping,/^127\.0\.0\.1:\d+$/);return 'postgres://upgrade_test:'+password+'@'+mapping+'/omni_upgrade_test?sslmode=disable';};
 let database=await dsn(),dbName='omni_upgrade_test';
 const sql=q=>docker('exec',container,'psql','-X','-q','-t','-A','-v','ON_ERROR_STOP=1','-U','upgrade_test','-d',dbName,'-c',q);
 const snapshot=async()=>{const result={};for(const table of ['conversations','messages','desk_read_receipts','knowledge_management'])result[table]=JSON.parse(await sql("SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb) FROM customer_service."+table+" t"));result.message_sequence=await sql('SELECT last_value,is_called FROM customer_service.messages_sequence_no_seq');return result;};
 await run(join(output,'old-migrate'),[],root,{DATABASE_URL:database});assert.equal(await sql('SELECT version FROM public.customer_service_schema_version'),'3');
 const gatewayPort=await port(),origin='http://127.0.0.1:'+gatewayPort,hostSecret=randomBytes(32).toString('hex'),agentSecret=randomBytes(32).toString('hex');
 const gatewayEnv=()=>({DATABASE_URL:database,WEBCHAT_ADDRESS:'127.0.0.1:'+gatewayPort,WEBCHAT_HOST_ID:'upgrade-fixture',WEBCHAT_HOST_SECRET:hostSecret,WEBCHAT_REPLY_MODE:'fake',WEBCHAT_AGENTS_JSON:JSON.stringify([{source_id:'upgrade-fixture',id:'fixture-agent',secret:agentSecret}])});
 const startGateway=async prefix=>{gateway=child(join(output,prefix+'-webchat-server'),[],root,gatewayEnv());await wait(async()=>gateway.exitCode===null&&(await fetch(origin+'/readyz')).status===200);};
 const mint=customer=>json(origin,'/internal/chat/sessions',{customer_id:customer},bearer(hostSecret),201);
 const history=s=>json(origin,'/chat/v1/messages',undefined,bearer(s.access_token));
 const send=(s,event,content)=>json(origin,'/chat/v1/messages',{event_id:event,content},bearer(s.access_token));
 const receipt=(s,event,content)=>json(origin,'/chat/v1/message-receipt',{event_id:event,content},bearer(s.access_token));
 const desk=(s,action,extra={})=>json(origin,'/internal/chat/desk/action',{conversation_id:s.conversation_id,action,...extra},bearer(agentSecret));
 await startGateway('old');
 const states=[];
 for(const kind of ['bot','waiting','active']){
  const s=await mint('fictional-'+kind);let sent;
  if(kind!=='bot')await json(origin,'/chat/v1/handoff',{},bearer(s.access_token));
  sent=await send(s,'old-'+kind,'fictional '+kind+' question');
  if(kind==='active'){const row=await desk(s,'history');await desk(s,'claim',{revision:row.revision});await desk(s,'reply',{event_id:'old-human',content:'fictional old human reply'});}
  if(kind!=='bot')await desk(s,'read',{message_ids:[sent.message_id]});
  states.push({kind,s,sent,history:await history(s),receipt:await receipt(s,'old-'+kind,'fictional '+kind+' question')});
 }
 assert.equal(states[0].history.status,'bot_active');assert.equal(states[1].history.status,'waiting_human');assert.equal(states[2].history.status,'human_active');
 const adminPort=await port(),adminOrigin='http://127.0.0.1:'+adminPort,accountFile=join(output,'accounts.json'),adminPassword='fictional-upgrade-admin-only';
 const hash=await run(join(output,'new-knowledge-admin'),['-hash-password'],root,{},adminPassword+'\n');
 await fs.writeFile(accountFile,JSON.stringify([{id:'fixture',password_hash:hash,permissions:['knowledge.read','knowledge.edit','knowledge.publish']}]),{mode:0o600});
 const startAdmin=async(prefix='new')=>{admin=child(join(output,prefix+'-knowledge-admin'),[],root,{DATABASE_URL:database,KNOWLEDGE_ADMIN_ADDRESS:'127.0.0.1:'+adminPort,KNOWLEDGE_ADMIN_ORIGIN:adminOrigin,KNOWLEDGE_ADMIN_ACCOUNTS_FILE:accountFile});await wait(async()=>admin.exitCode===null&&(await fetch(adminOrigin+'/')).status===200);};
 let cookie;
 const login=async()=>{const r=await http(adminOrigin,'/auth/login',{account:'fixture',password:adminPassword},{Origin:adminOrigin});cookie=r.headers.get('set-cookie').split(';')[0];};
 const manage=(path,body)=>json(adminOrigin,path,body,{Origin:adminOrigin,Cookie:cookie});
 await startAdmin('old');await login();
 await manage('/internal/knowledge/draft',{revision:0,entries:[{id:'hours',title:'Hours',source:'fictional',content:'Fictional 09-17',keywords:['hours']}]});await manage('/internal/knowledge/publish',{revision:1});await manage('/internal/knowledge/draft',{revision:2,entries:[{id:'hours',title:'Hours',source:'fictional',content:'Unpublished old draft',keywords:['hours']}]});const oldKnowledge=await manage('/internal/knowledge/state');assert.equal(oldKnowledge.revision,3);await stop(admin);

 const before=await snapshot();assert.equal(before.conversations.length,3);assert.ok(before.messages.length>=5);assert.equal(before.desk_read_receipts.length,2);
 await fs.writeFile(join(output,'before.json'),JSON.stringify(before,null,2));await mark('v1_1_0_created_three_states_messages_receipts_and_knowledge');
 await stop(gateway);const backup=await docker('exec',container,'pg_dump','-U','upgrade_test','-d','omni_upgrade_test','--no-owner','--no-privileges');assert.ok(backup.includes('knowledge_management'));await mark('schema_3_backup_created_before_upgrade');await run(join(output,'new-migrate'),[],root,{DATABASE_URL:database});assert.equal(await sql('SELECT version FROM public.customer_service_schema_version'),'4');assert.deepEqual(await snapshot(),before);await mark('schema_3_to_4_preserves_all_old_rows_and_sequence');
 await run(join(output,'new-migrate'),[],root,{DATABASE_URL:database});assert.deepEqual(await snapshot(),before);await mark('repeat_migration_preserves_old_data');
 const oldCheck=child(join(output,'old-webchat-server'),['-check-resources'],root,gatewayEnv());assert.notEqual((await oldCheck.done).code,0);await mark('old_application_rejects_schema_4');
 await startGateway('new');
 for(const item of states){const s=await mint('fictional-'+item.kind);assert.equal(s.conversation_id,item.s.conversation_id);item.s=s;assert.deepEqual(await history(s),item.history);assert.deepEqual(await receipt(s,'old-'+item.kind,'fictional '+item.kind+' question'),item.receipt);}
 assert.deepEqual(await snapshot(),before);await mark('new_gateway_reads_same_history_and_message_receipts');
 await send(states[0].s,'new-bot','fictional new question');
 const waiting=states[1].s,row=await desk(waiting,'history');await desk(waiting,'claim',{revision:row.revision});await desk(waiting,'reply',{event_id:'new-human-waiting',content:'fictional continued reply'});
 const active=states[2].s;await desk(active,'reply',{event_id:'new-human-active',content:'fictional continuation'});const activeRow=await desk(active,'history');await desk(active,'close',{revision:activeRow.revision});
 assert.equal((await history(waiting)).status,'human_active');assert.equal((await history(active)).status,'closed');await mark('old_conversations_continue_bot_claim_reply_and_close');
 await startAdmin();await login();assert.deepEqual(await manage('/internal/knowledge/state'),oldKnowledge);
 await manage('/internal/knowledge/publish',{revision:3});const knowledge=await manage('/internal/knowledge/state');assert.equal(knowledge.revision,4);
 await mark('upgraded_database_supports_authenticated_knowledge_publish');
 const after=await snapshot();const histories=await Promise.all(states.map(i=>history(i.s)));
 await fs.writeFile(join(output,'after.json'),JSON.stringify(after,null,2));
 await stop(gateway);await stop(admin);await docker('restart',container);await ready();database=await dsn();await startGateway('new');await startAdmin();
 assert.deepEqual(await snapshot(),after);await http(adminOrigin,'/auth/me',undefined,{Cookie:cookie},401);await login();assert.deepEqual(await manage('/internal/knowledge/state'),knowledge);
 for(let i=0;i<states.length;i++){
  // Minting a new capability for a closed consultation may start a new consultation,
  // so compare closed history via the persisted rows above; active histories via HTTP.
  if(histories[i].status==='closed')continue;
  const s=await mint('fictional-'+states[i].kind);assert.equal(s.conversation_id,states[i].s.conversation_id);assert.deepEqual(await history(s),histories[i]);
 }
 await mark('process_and_database_restart_preserves_rows_knowledge_and_active_history');
 await stop(gateway);await stop(admin);
 await docker('exec',container,'createdb','-U','upgrade_test','omni_restore_test');
 await run('docker',['--context','desktop-linux','exec','-i',container,'psql','-X','-q','-v','ON_ERROR_STOP=1','-U','upgrade_test','-d','omni_restore_test'],root,{},backup);
 dbName='omni_restore_test';database=database.replace('/omni_upgrade_test?','/omni_restore_test?');
 assert.equal(await sql('SELECT version FROM public.customer_service_schema_version'),'3');assert.deepEqual(await snapshot(),before);
 assert.equal(await sql("SELECT to_regclass('customer_service.knowledge_feedback') IS NULL"),'t');
 await startGateway('old');await startAdmin('old');await login();assert.deepEqual(await manage('/internal/knowledge/state'),oldKnowledge);
 for(const item of states){const s=await mint('fictional-'+item.kind);assert.deepEqual(await history(s),item.history);}
 await mark('backup_restored_to_separate_schema_3_database_and_old_apps_work');
 report.status='passed';
}catch(e){report.status='failed';report.failure=e.message?.replace(/postgres:\/\/[^\s]+/g,'[redacted]');process.exitCode=1;}
finally{
 for(const p of [...children])await stop(p);
 for(const [kind,name,present] of [['container',container,created],['volume',volume,volumeCreated]])if(present){try{const inspect=kind==='container'?['inspect']:['volume','inspect'];const format=kind==='container'?'{{index .Config.Labels "omni.feedback-upgrade"}}':'{{index .Labels "omni.feedback-upgrade"}}';assert.equal(await docker(...inspect,'--format',format,name),id);await docker(...(kind==='container'?['rm','-f',name]:['volume','rm',name]));report.cleanup.push({kind,status:'removed'});}catch{report.cleanup.push({kind,status:'failed'});report.status='failed';process.exitCode=1;}}
 await fs.rm(join(output,'accounts.json'),{force:true});await save();console.log(report.status.toUpperCase()+' '+join(output,'report.json'));
}
