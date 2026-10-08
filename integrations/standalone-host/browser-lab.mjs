// Isolated local PostgreSQL acceptance. No environment files or model calls.
import fs from 'node:fs';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import http from 'node:http';
import readline from 'node:readline';
const root=fileURLToPath(new URL('../../',import.meta.url));
if(process.argv.length!==3||process.argv[2]!=='--allow-local-postgres')throw Error('Requires --allow-local-postgres; creates dedicated local test database/volume');
const compose='deploy/standalone-host/compose.yaml';
const dsn='postgres://standalone_demo:standalone_local_test@127.0.0.1:15434/standalone_chat_acceptance?sslmode=disable';
const runID=new Date().toISOString().replace(/[:.]/g,'-')+'-'+randomBytes(4).toString('hex');
const directory=await mkdtemp(join(tmpdir(),'omni-standalone-pg-'));
const output=root+'examples/archive/integration/standalone-host/'+runID;
fs.mkdirSync(output,{recursive:true});
const report={started_at:new Date().toISOString(),passed:null,review_status:'pending_manual_browser_review',storage:'postgres',reply_mode:'fake',external_model_calls:0,source_id:'browser-check-'+runID,checks:[],processes:[],database_retained:true};
const save=()=>fs.writeFileSync(output+'/report.json',JSON.stringify(report,null,2)+'\n',{mode:0o600});
function ensure(ok,name){report.checks.push({name,passed:!!ok});save();if(!ok)throw Error(name);}
const run=promisify(execFile);
const env={PATH:process.env.PATH};
const docker=(...args)=>run('docker',['--context','desktop-linux','compose','--env-file','/dev/null','-f',compose,...args],{cwd:root,env:{...env,HOME:process.env.HOME},timeout:120000,maxBuffer:1024*1024});
const secret=randomBytes(32).toString('base64url');
let gateway,host,proxy,dbStarted=false;
const children=[];
async function launch(binary,args,environment,pattern,role){
  const child=spawn(binary,args,{cwd:root,env:environment,stdio:['ignore','pipe','pipe']});
  const record={role,pid:child.pid,stopped:false};report.processes.push(record);
  const entry={child,record,closed:new Promise(resolve=>child.once('close',(code,signal)=>{record.exit_code=code;record.signal=signal;resolve();}))};children.push(entry);
  child.stderr.resume();
  entry.origin=await new Promise((resolve,reject)=>{
    const timer=setTimeout(()=>reject(Error('startup_timeout')),20000);let text='';
    child.once('error',()=>{clearTimeout(timer);reject(Error('startup_error'));});
    child.once('exit',()=>{clearTimeout(timer);reject(Error('startup_exit'));});
    child.stdout.on('data',chunk=>{text+=chunk;const match=text.match(pattern);if(match){clearTimeout(timer);resolve(match[1]);}});
  });save();return entry;
}
async function stop(entry,signal='SIGTERM'){
  if(!entry)return;
  if(entry.child.exitCode===null&&entry.child.signalCode===null){entry.child.kill(signal);const timer=setTimeout(()=>entry.child.kill('SIGKILL'),7000);try{await entry.closed;}finally{clearTimeout(timer);}}
  else await entry.closed;
  entry.record.stopped=true;save();
}
const startGateway=async(port=0)=>{
  const entry=await launch(join(directory,'gateway'),['-address','127.0.0.1:'+port],{...env,DATABASE_URL:dsn,WEBCHAT_HOST_ID:report.source_id,WEBCHAT_HOST_SECRET:secret,WEBCHAT_REPLY_MODE:'fake'},/Web chat server: (http:\/\/127\.0\.0\.1:\d+)\n/,'gateway');
  const ready=await fetch(entry.origin+'/readyz',{signal:AbortSignal.timeout(5000)});ensure(ready.status===200,'gateway_ready');await ready.body?.cancel();return entry;
};
const startBackend=(port=0)=>launch(process.execPath,['integrations/standalone-host/host-worker.mjs'],{...env,STANDALONE_GATEWAY_ORIGIN:gateway.origin,STANDALONE_HOST_SECRET:secret,STANDALONE_PUBLIC_PORT:String(port)},/^(http:\/\/127\.0\.0\.1:\d+)\n/,'business-backend');

try {
  save();await docker('up','-d','--wait','postgres');dbStarted=true;
  await run('go',['build','-o',join(directory,'migrate'),'./cmd/migrate'],{cwd:root,timeout:120000});
  await run('go',['build','-o',join(directory,'gateway'),'./cmd/webchat-server'],{cwd:root,timeout:120000});
  await run(join(directory,'migrate'),[],{cwd:root,env:{...env,DATABASE_URL:dsn},timeout:30000});
  gateway=await startGateway();host=await startBackend();
  const gatewayPort=Number(new URL(gateway.origin).port),hostPort=Number(new URL(host.origin).port);
  let publicOrigin,dropNext=false;
  report.browser_network=[];
  proxy=http.createServer(async(req,res)=>{
    try {
      if(req.headers.host!==new URL(publicOrigin).host||(req.headers.origin&&req.headers.origin!==publicOrigin)){res.writeHead(403).end();return;}
      const chunks=[];for await(const chunk of req)chunks.push(chunk);
      const body=Buffer.concat(chunks);
      const headers={};for(const name of ['content-type','cookie','authorization'])if(req.headers[name])headers[name]=req.headers[name];
      if(req.headers.origin)headers.Origin=host.origin;
      const upstream=await fetch(host.origin+req.url,{method:req.method,headers,body:['GET','HEAD'].includes(req.method)?undefined:body,redirect:'manual',signal:AbortSignal.timeout(25000)});
      const data=Buffer.from(await upstream.arrayBuffer());
      const entry={method:req.method,path:req.url,status:upstream.status};
      if(req.url==='/chat/v1/messages'&&req.method==='POST'){
        try{const sent=JSON.parse(body);entry.event_id=sent.event_id;entry.content=sent.content;const result=JSON.parse(data);entry.message_id=result.message_id;entry.reply_message_id=result.reply_message_id;}catch{}
      }
      if(req.url==='/host/chat-session'&&upstream.status===200){const result=JSON.parse(data);entry.user=result.user?.id;entry.conversation=result.conversation_id;}
      if(req.url==='/chat/v1/messages'&&req.method==='POST'&&dropNext&&upstream.status===200){dropNext=false;entry.response_dropped=true;report.browser_network.push(entry);save();res.destroy();return;}
      report.browser_network.push(entry);save();
      for(const name of ['content-type','cache-control','content-security-policy','x-content-type-options','referrer-policy','set-cookie']){const value=upstream.headers.get(name);if(value)res.setHeader(name,value);}
      res.writeHead(upstream.status);res.end(data);
    }catch{res.writeHead(502,{'Content-Type':'application/json'}).end(JSON.stringify({error:'unavailable'}));}
  });
  await new Promise(resolve=>proxy.listen(0,'127.0.0.1',resolve));publicOrigin='http://127.0.0.1:'+proxy.address().port;
  report.browser_origin=publicOrigin;save();
  console.log(JSON.stringify({browser_origin:publicOrigin,report:output+'/report.json',commands:['restart-gateway','restart-backend','drop-next','stop']}));
  const lines=readline.createInterface({input:process.stdin});
  for await(const line of lines){
    const command=line.trim();
    if(command==='stop')break;
    if(command==='restart-gateway'){await stop(gateway,'SIGKILL');gateway=await startGateway(gatewayPort);}
    else if(command==='restart-backend'){await stop(host,'SIGKILL');host=await startBackend(hostPort);}
    else if(command==='drop-next')dropNext=true;
    else {console.log('unknown command');continue;}
    report.checks.push({name:command,performed_at:new Date().toISOString()});save();console.log(command+': ready');
  }
  lines.close();report.finished_at=new Date().toISOString();
}catch{report.passed=false;report.failure_step='browser_lab';process.exitCode=1;}
finally{
  if(proxy){proxy.closeAllConnections();await new Promise(resolve=>proxy.close(resolve));}
  for(const entry of children.slice().reverse())await stop(entry);
  if(dbStarted){await docker('stop','postgres');report.database_stopped=true;}
  await rm(directory,{recursive:true,force:true});save();console.log('Browser lab stopped; database retained.');
}
