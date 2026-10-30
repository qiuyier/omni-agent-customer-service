#!/usr/bin/env node
// Local regression only: no .env, deployment credentials, external models or existing DB.
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomUUID} from 'node:crypto';
import {parseArgs,assess,cleanEnvironment} from './recovery_check_support.mjs';

let options;
try{options=parseArgs(process.argv.slice(2));}catch(e){console.error(e.message);process.exit(2);}
if(options.help){console.log('node scripts/recovery_check.mjs [--postgres --allow-test-writes]\nDefault: JS + Go race tests. PostgreSQL: disposable localhost tmpfs container only. Reports retained in printed temporary directory.');process.exit(0);}
const root=fileURLToPath(new URL('../',import.meta.url));
const output=await fs.mkdtemp(join(tmpdir(),'omni-recovery-check-'));
const env=cleanEnvironment(process.env);
const report={started_at:new Date().toISOString(),status:'running',external_models:'not_configured',suites:[],manual_required:[
 '真实浏览器：跨标签页身份切换、重新登录后的显示和待重试入口',
 '真实浏览器：页面隐藏/恢复与无人操作网络恢复（逻辑测试不能替代页面实测）',
],cleanup:[],postgres:options.postgres?'requested':'not_run'};
const save=()=>fs.writeFile(join(output,'report.json'),JSON.stringify(report,null,2)+'\n');
console.log('Report directory: '+output);await save();
let interrupted=false,active;
const signalGroup=(child,signal)=>{if(!child.pid)return;try{process.kill(-child.pid,signal);}catch(e){if(e.code!=='ESRCH')throw e;}};
const interrupt=()=>{interrupted=true;if(active)signalGroup(active,'SIGTERM');};
process.on('SIGINT',interrupt);process.on('SIGTERM',interrupt);
async function run(command,args,{name,extra={},timeout=240000,cleanup=false}={}){
 if(interrupted&&!cleanup)throw Error('interrupted');
 const log=join(output,name+'.log');const chunks=[];
 let timedOut=false,spawnError;
 const child=spawn(command,args,{cwd:root,env:{...env,...extra},detached:true,stdio:['ignore','pipe','pipe']});active=child;
 child.stdout.on('data',b=>chunks.push(b));child.stderr.on('data',b=>chunks.push(b));
 child.on('error',e=>spawnError=e.code||'spawn_error');
 const timer=setTimeout(()=>{timedOut=true;signalGroup(child,'SIGTERM');},timeout);
 // Also reap orphaned grandchildren on normal completion or interruption.
 const killer=setInterval(()=>{if(timedOut||(interrupted&&!cleanup))signalGroup(child,'SIGKILL');},7000);
 const result=await new Promise(resolve=>child.once('close',(code,signal)=>resolve({code,signal})));
 clearTimeout(timer);clearInterval(killer);if(child.pid)signalGroup(child,'SIGKILL');active=null;
 const text=Buffer.concat(chunks).toString();await fs.writeFile(log,text);
 return {...result,text,timedOut,spawnError,log};
}
async function suite(id,command,args,format,extra={}){
 console.log('RUN '+id);
 const result=await run(command,args,{name:id,extra});
 const outcome=assess(result,format);
 report.suites.push({id,...outcome,log:result.log});await save();console.log(outcome.status.toUpperCase()+' '+id);
}
const container='omni-recovery-'+randomUUID();let created=false;
async function docker(args,name,cleanup=false){return run('docker',['--context','desktop-linux',...args],{name,cleanup,timeout:60000});}
try{
 const web=(await fs.readdir(join(root,'web/chat/src'))).filter(x=>x.endsWith('.test.mjs')).sort().map(x=>'web/chat/src/'+x);
 await suite('web-and-host',process.execPath,['--test','--test-reporter=tap',...web,'integrations/standalone-host/host.test.mjs','integrations/standalone-host/inflight-identity.test.mjs','integrations/standalone-host/lab-options.test.mjs'],'tap');
 await suite('gateway-recovery','go',['test','-json','-race','-count=1','-timeout=120s','./internal/webchat','-run','^(TestReceiptReplyRecoveryEligibility|TestRetrySnapshotDoesNotAuthorizeAfterHandoff|TestTwoSessionsRetrySameFailedEvent|TestRetryAcrossHumanLifecycle)$'],'go');
 await suite('knowledge-core','go',['test','-json','-race','-count=1','-timeout=120s','./internal/knowledge/manage','./internal/knowledge/faq'],'go');
 if(options.postgres){
  // Mark ownership before starting: even an interrupted docker run may create it.
  created=true;
  const started=await docker(['run','-d','--name',container,'--label','omni.recovery-check=temporary','--tmpfs','/var/lib/postgresql/data','-e','POSTGRES_USER=recovery_test','-e','POSTGRES_PASSWORD=local_recovery_only','-e','POSTGRES_DB=recovery_check_test','-p','127.0.0.1::5432','postgres:16-alpine'],'postgres-start');
  if(started.code!==0)throw Error('postgres_start_failed; see postgres-start.log');
  const port=await docker(['port',container,'5432/tcp'],'postgres-port');
  const match=port.text.trim().match(/^127\.0\.0\.1:(\d+)$/);if(port.code!==0||!match)throw Error('invalid_local_postgres_port');
  let ready=false;
  for(let i=0;i<30&&!ready;i++){
   ready=(await docker(['exec',container,'pg_isready','-U','recovery_test','-d','recovery_check_test'],`postgres-ready-${i}`)).code===0;
   if(!ready)await new Promise(r=>setTimeout(r,500));
  }
  if(!ready)throw Error('postgres_not_ready');
  const dsn=`postgres://recovery_test:local_recovery_only@127.0.0.1:${match[1]}/recovery_check_test?sslmode=disable`;
  await suite('postgres-recovery','go',['test','-json','-race','-count=3','-timeout=120s','./internal/persistence/postgres','-run','^(TestDatabaseRetryAcrossHumanLifecycle|TestKnowledgeDatabaseLifecycle)$'],'go',{TEST_DATABASE_URL:dsn});
  report.postgres='executed';
 }
}catch(e){report.error=e.message;}
finally{
 if(created){
  try{
   const removed=await docker(['rm','-f',container],'postgres-cleanup',true);
   const absent=removed.code===0||/No such container/.test(removed.text);
   report.cleanup.push({resource:container,status:absent?'passed':'failed',log:removed.log});
  }catch(e){report.cleanup.push({resource:container,status:'failed',error:e.message});}
 }
 report.status=interrupted?'interrupted':report.error||report.suites.some(s=>s.status!=='passed')||report.cleanup.some(c=>c.status!=='passed')?'failed':'passed';
 report.scope='automated_only; manual_required is not certified by this run';
 report.finished_at=new Date().toISOString();await save();
 console.log(`${report.status.toUpperCase()} automated checks; manual items remain. ${join(output,'report.json')}`);
 process.exitCode=report.status==='passed'?0:1;
}
