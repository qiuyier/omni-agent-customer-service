// Explicit local PostgreSQL + real password-login browser fixture. No .env.
import fs from 'node:fs';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createInterface} from 'node:readline';
import {startResponseGate} from './response-gate.mjs';
if(process.argv.length!==3||process.argv[2]!=='--allow-local-postgres')throw Error('requires --allow-local-postgres');
const root=fileURLToPath(new URL('../../',import.meta.url)),run=promisify(execFile);
const random=()=>randomBytes(32).toString('base64url'),id=new Date().toISOString().replace(/[:.]/g,'-')+'-'+randomBytes(4).toString('hex');
const directory=await mkdtemp(join(tmpdir(),'omni-office-pg-')),output=root+'examples/archive/integration/office-portal/'+id;
fs.mkdirSync(output,{recursive:true});
const report={source_id:'office-restart-'+id,storage:'postgres',reply_mode:'fake',processes:[],receipts:[],events:[],browser_verification:'separate_manual_report'};
const save=()=>fs.writeFileSync(output+'/report.json',JSON.stringify(report,null,2)+'\n',{mode:0o600});
const env={PATH:process.env.PATH},dsn='postgres://standalone_demo:standalone_local_test@127.0.0.1:15434/standalone_chat_acceptance?sslmode=disable';
const docker=(...args)=>run('docker',['--context','desktop-linux','compose','--env-file','/dev/null','-f','deploy/standalone-host/compose.yaml',...args],{cwd:root,env:{...env,HOME:process.env.HOME},timeout:120000,maxBuffer:1048576});
const hostSecret=random(),agentSecret=random();
const accounts=[{realm:'member',handle:'lin',number:'MEM:731',passphrase:random()},{realm:'member',handle:'zhou',number:'MEM:864',passphrase:random()},{realm:'operator',handle:'staff',number:'OPS:42',passphrase:random()}];
let gateway,backend,gate,dbStarted=false,step='setup';const children=[];
async function launch(binary,args,environment,pattern,role){
 const child=spawn(binary,args,{cwd:root,env:environment,stdio:['ignore','pipe','pipe']});
 const record={role,pid:child.pid,stopped:false};report.processes.push(record);
 const entry={child,record,closed:new Promise(resolve=>child.once('close',(code,signal)=>{record.exit_code=code;record.signal=signal;resolve();}))};children.push(entry);child.stderr.resume();
 entry.origin=await new Promise((resolve,reject)=>{
  let out='';const timer=setTimeout(()=>reject(Error('startup_timeout')),20000);
  const fail=()=>{clearTimeout(timer);reject(Error('startup_failed'));};child.once('error',fail);child.once('exit',fail);
  child.stdout.on('data',chunk=>{out+=chunk;const match=out.match(pattern);if(match){clearTimeout(timer);resolve(match[1]);}});
 });save();return entry;
}
async function stop(entry,signal='SIGTERM'){
 if(!entry)return;if(entry.child.exitCode===null&&entry.child.signalCode===null){entry.child.kill(signal);const timer=setTimeout(()=>entry.child.kill('SIGKILL'),7000);try{await entry.closed;}finally{clearTimeout(timer);}}else await entry.closed;
 entry.record.stopped=true;save();
}
async function startGateway(port=0){
 const entry=await launch(join(directory,'gateway'),['-address','127.0.0.1:'+port],{...env,DATABASE_URL:dsn,WEBCHAT_HOST_ID:report.source_id,WEBCHAT_HOST_SECRET:hostSecret,WEBCHAT_REPLY_MODE:'fake',WEBCHAT_AGENTS_JSON:JSON.stringify([{source_id:report.source_id,id:'staff-1',secret:agentSecret}])},/Web chat server: (http:\/\/127\.0\.0\.1:\d+)\n/,'gateway');
 const ready=await fetch(entry.origin+'/readyz',{signal:AbortSignal.timeout(5000)});await ready.body?.cancel();if(ready.status!==200)throw Error('not_ready');return entry;
}
const startBackend=(port=0)=>launch(process.execPath,['integrations/office-portal/persistence-worker.mjs'],{...env,OFFICE_TEST_CONFIG:JSON.stringify({gatewayOrigin:gateway.origin,hostSecret,agents:[{id:'staff-1',name:'办公服务专员',secret:agentSecret}],accounts,port})},/^(http:\/\/127\.0\.0\.1:\d+)\n/,'office-backend');
try{
 step='database';if((await docker('ps','--status','running','-q','postgres')).stdout.trim())throw Error('dedicated_database_in_use');
 dbStarted=true;await docker('up','-d','--wait','postgres');
 step='build';for(const name of ['migrate','webchat-server'])await run('go',['build','-o',join(directory,name==='migrate'?'migrate':'gateway'),'./cmd/'+name],{cwd:root,timeout:120000});
 step='migrate';await run(join(directory,'migrate'),[],{cwd:root,env:{...env,DATABASE_URL:dsn},timeout:30000});
 step='startup';gateway=await startGateway();backend=await startBackend();
 gate=await startResponseGate(backend.origin,{notify:event=>{report.events.push({event,at:new Date().toISOString()});save();console.log(event);},observeReceipt:receipt=>{report.receipts.push(receipt);save();console.log('message_receipt_recorded');}});
 console.log(JSON.stringify({origin:gate.origin,testAccounts:accounts.map(({realm,handle,passphrase})=>({realm,handle,passphrase})),commands:'restart-gateway | restart-backend | lose-send | status | stop',report:output+'/report.json'}));
 const input=createInterface({input:process.stdin});
 for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>input.close());
 step='interactive';
 for await(const line of input){
  const command=line.trim();
  if(command==='stop')break;
  if(command==='restart-gateway'){const port=Number(new URL(gateway.origin).port);await stop(gateway,'SIGKILL');gateway=await startGateway(port);}
  else if(command==='restart-backend'){const port=Number(new URL(backend.origin).port);await stop(backend,'SIGKILL');backend=await startBackend(port);}
  else if(command==='lose-send')gate.loseNextSend();
  else if(command==='status'){console.log(JSON.stringify({receiptCount:report.receipts.length,events:report.events.map(x=>x.event)}));continue;}
  else{console.log('invalid command');continue;}
  report.events.push({event:command,at:new Date().toISOString()});save();console.log(command+' complete');
 }
 report.run_completed=true;
}catch{report.failure_step=step;process.exitCode=1;console.log('test_lab_failed: '+step);}
finally{
 process.stdin.pause();
 await gate?.close();
 for(const entry of children.slice().reverse())await stop(entry);
 if(dbStarted)try{await docker('stop','postgres');report.database_stopped=true;}catch{report.database_stopped=false;process.exitCode=1;}
 await rm(directory,{recursive:true,force:true});report.finished_at=new Date().toISOString();save();console.log(JSON.stringify({report:output+'/report.json',database_stopped:report.database_stopped}));
}
