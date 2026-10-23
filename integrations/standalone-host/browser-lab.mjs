// Isolated local acceptance. Real model calls require an additional explicit flag.
import fs from 'node:fs';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';
import {spawn,execFile} from 'node:child_process';
import {promisify,parseEnv} from 'node:util';
import http from 'node:http';
import readline from 'node:readline';
import {labOptions} from './lab-options.mjs';
const root=fileURLToPath(new URL('../../',import.meta.url));
const {ai,human,maxExternalCalls,mockSlow=false}=labOptions(process.argv.slice(2));
// Parse data, never source shell code. Only explicitly selected model settings
// enter the gateway. Database/host/knowledge settings cannot come from .env.
const aiEnv={};
if(ai&&!mockSlow){
  const parsed=parseEnv(fs.readFileSync(root+'.env','utf8'));
  for(const key of ['AI_PROVIDER','AI_PROTOCOL','AI_MODEL','AI_BASE_URL','AI_API_KEY'])if(parsed[key])aiEnv[key]=parsed[key];
  if(!aiEnv.AI_API_KEY||!aiEnv.AI_MODEL||aiEnv.AI_PROTOCOL!=='chat_completions')throw Error('AI lab requires explicit model, generic key and chat_completions in .env');
  const endpoint=new URL(aiEnv.AI_BASE_URL);
  if(endpoint.protocol!=='https:'||endpoint.username||endpoint.password||endpoint.search||endpoint.hash)throw Error('AI lab requires clean HTTPS endpoint');
}
if(mockSlow)Object.assign(aiEnv,{AI_PROVIDER:'custom',AI_PROTOCOL:'chat_completions',AI_MODEL:'local-slow-mock',AI_API_KEY:randomBytes(32).toString('hex'),AI_BASE_URL:'http://127.0.0.1'});
const compose='deploy/standalone-host/compose.yaml';
const dsn='postgres://standalone_demo:standalone_local_test@127.0.0.1:15434/standalone_chat_acceptance?sslmode=disable';
const runID=new Date().toISOString().replace(/[:.]/g,'-')+'-'+randomBytes(4).toString('hex');
const directory=await mkdtemp(join(tmpdir(),'omni-standalone-pg-'));
const output=root+'examples/archive/integration/standalone-host/'+runID;
fs.mkdirSync(output,{recursive:true});
const report={started_at:new Date().toISOString(),passed:null,review_status:'pending_manual_browser_review',storage:'postgres',reply_mode:ai?'ai':'fake',external_model_calls:0,model:ai?aiEnv.AI_MODEL:undefined,model_network:[],source_id:'browser-check-'+runID,checks:[],processes:[],database_retained:true};
const save=()=>fs.writeFileSync(output+'/report.json',JSON.stringify(report,null,2)+'\n',{mode:0o600});
function ensure(ok,name){report.checks.push({name,passed:!!ok});save();if(!ok)throw Error(name);}
const run=promisify(execFile);
const env={PATH:process.env.PATH};
const docker=(...args)=>run('docker',['--context','desktop-linux','compose','--env-file','/dev/null','-f',compose,...args],{cwd:root,env:{...env,HOME:process.env.HOME},timeout:120000,maxBuffer:1024*1024});
const secret=randomBytes(32).toString('base64url');
const agents=human?[{id:'qiao',name:'乔杉',secret:randomBytes(32).toString('base64url')},{id:'wen',name:'温宁',secret:randomBytes(32).toString('base64url')}]:[];
report.human_enabled=human;
report.mock_slow_ai=mockSlow;
report.mock_requests=0;
const pendingMock=new Set();
let mockDelayMs=90000;
report.max_external_model_calls=maxExternalCalls;
const faqFile=human?'ai-human-faq.json':'ai-faq.json';
const agentEnv={WEBCHAT_AGENTS_JSON:JSON.stringify(agents.map(a=>({id:a.id,source_id:report.source_id,secret:a.secret}))),STANDALONE_AGENTS_JSON:JSON.stringify(agents)};
let gateway,host,proxy,modelProxy,modelDown=false,dbStarted=false;
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
  const entry=await launch(join(directory,'gateway'),['-address','127.0.0.1:'+port],{...env,...aiEnv,WEBCHAT_AGENTS_JSON:agentEnv.WEBCHAT_AGENTS_JSON,DATABASE_URL:dsn,WEBCHAT_HOST_ID:report.source_id,WEBCHAT_HOST_SECRET:secret,WEBCHAT_REPLY_MODE:ai?'ai':'fake',...(ai?{KNOWLEDGE_FAQ_PATH:root+'integrations/standalone-host/'+faqFile,KNOWLEDGE_RETRIEVER:'keyword'}:{})},/Web chat server: (http:\/\/127\.0\.0\.1:\d+)\n/,'gateway');
  const ready=await fetch(entry.origin+'/readyz',{signal:AbortSignal.timeout(5000)});ensure(ready.status===200,'gateway_ready');await ready.body?.cancel();return entry;
};
const startBackend=(port=0)=>launch(process.execPath,['integrations/standalone-host/host-worker.mjs'],{...env,STANDALONE_AGENTS_JSON:agentEnv.STANDALONE_AGENTS_JSON,STANDALONE_GATEWAY_ORIGIN:gateway.origin,STANDALONE_HOST_SECRET:secret,STANDALONE_REPLY_MODE:ai?'ai':'fake',STANDALONE_PUBLIC_PORT:String(port)},/^(http:\/\/127\.0\.0\.1:\d+)\n/,'business-backend');

try {
  if(ai){
    const endpoint=aiEnv.AI_BASE_URL.replace(/\/$/,'');
    modelProxy=http.createServer(async(req,res)=>{
      const started=Date.now();
      const record={started_at:new Date().toISOString(),injected_failure:modelDown};
      try{
        if(req.method!=='POST'||req.url!=='/chat/completions'||req.headers.authorization!=='Bearer '+aiEnv.AI_API_KEY){res.writeHead(403).end();return;}
        let size=0;const chunks=[];for await(const chunk of req){size+=chunk.length;if(size>1048576)throw Error('request too large');chunks.push(chunk);}
        if(modelDown){record.status=503;res.writeHead(503,{'Content-Type':'application/json'}).end(JSON.stringify({error:{message:'local injected failure',type:'unavailable'}}));return;}
        if(mockSlow){
          record.mock=true;record.mock_delay_ms=mockDelayMs;report.mock_requests++;save();console.log('mock-model: waiting up to '+mockDelayMs+'ms or release-model');
          await new Promise(resolve=>{
            const finish=()=>{clearTimeout(timer);pendingMock.delete(finish);res.off('close',finish);resolve();};
            const timer=setTimeout(finish,mockDelayMs);
            pendingMock.add(finish);res.once('close',finish);
          });
          if(res.destroyed){record.status=499;return;}
          record.status=200;
          res.writeHead(200,{'Content-Type':'application/json'}).end(JSON.stringify({id:'mock-delayed',object:'chat.completion',created:Math.floor(Date.now()/1000),model:'local-slow-mock',choices:[{index:0,message:{role:'assistant',content:'迟到的模拟 AI 回复，不应在人工接管后显示。'},finish_reason:'stop'}],usage:{prompt_tokens:1,completion_tokens:1,total_tokens:2}}));
          return;
        }
        if(report.external_model_calls>=maxExternalCalls){record.status=429;res.writeHead(429).end();return;}
        report.external_model_calls++;record.forwarded=true;save();
        const upstream=await fetch(endpoint+'/chat/completions',{method:'POST',headers:{Authorization:'Bearer '+aiEnv.AI_API_KEY,'Content-Type':'application/json'},body:Buffer.concat(chunks),redirect:'error',signal:AbortSignal.timeout(80000)});
        const data=Buffer.from(await upstream.arrayBuffer());record.status=upstream.status;
        if(!upstream.ok)modelDown=true; // Fail closed: SDK retries cannot add paid requests.
        res.writeHead(upstream.status,{'Content-Type':'application/json'}).end(data);
      }catch{if(record.forwarded)modelDown=true;record.status=502;if(!res.headersSent)res.writeHead(502,{'Content-Type':'application/json'}).end('{}');else res.end();}
      finally{record.elapsed_ms=Date.now()-started;report.model_network.push(record);save();}
    });
    await new Promise(resolve=>modelProxy.listen(0,'127.0.0.1',resolve));
    aiEnv.AI_BASE_URL='http://127.0.0.1:'+modelProxy.address().port;
    aiEnv.AI_ALLOW_INSECURE_HTTP='true'; // Only this local test fault proxy.
  }
  save();await docker('up','-d','--wait','postgres');dbStarted=true;
  await run('go',['build','-o',join(directory,'migrate'),'./cmd/migrate'],{cwd:root,timeout:120000});
  await run('go',['build','-o',join(directory,'gateway'),'./cmd/webchat-server'],{cwd:root,timeout:120000});
  await run(join(directory,'migrate'),[],{cwd:root,env:{...env,DATABASE_URL:dsn},timeout:30000});
  gateway=await startGateway();host=await startBackend();
  const gatewayPort=Number(new URL(gateway.origin).port),hostPort=Number(new URL(host.origin).port);
  let publicOrigin,dropNext=false,dropNextConsultation=false,failNextReceipt=false;
  report.browser_network=[];
  proxy=http.createServer(async(req,res)=>{
    try {
      if(req.headers.host!==new URL(publicOrigin).host||(req.headers.origin&&req.headers.origin!==publicOrigin)){res.writeHead(403).end();return;}
      const chunks=[];for await(const chunk of req)chunks.push(chunk);
      const body=Buffer.concat(chunks);
      const headers={};for(const name of ['content-type','cookie','authorization'])if(req.headers[name])headers[name]=req.headers[name];
      if(req.headers.origin)headers.Origin=host.origin;
      const upstream=await fetch(host.origin+req.url,{method:req.method,headers,body:['GET','HEAD'].includes(req.method)?undefined:body,redirect:'manual',signal:AbortSignal.timeout(ai?130000:25000)});
      const data=Buffer.from(await upstream.arrayBuffer());
      const entry={method:req.method,path:req.url,status:upstream.status};
      if(req.method==='POST'&&['/chat/v1/handoff','/chat/v1/consultations','/desk/action'].includes(req.url)){try{entry.request=JSON.parse(body);entry.result=JSON.parse(data);}catch{}}
      if(req.url==='/chat/v1/messages'&&req.method==='POST'){
        try{const sent=JSON.parse(body);entry.event_id=sent.event_id;entry.content=sent.content;const result=JSON.parse(data);entry.message_id=result.message_id;entry.reply_message_id=result.reply_message_id;entry.result=result;}catch{}
      }
      if(req.url==='/host/chat-session'&&upstream.status===200){const result=JSON.parse(data);entry.user=result.user?.id;entry.conversation=result.conversation_id;}
      if(req.url==='/chat/v1/messages'&&req.method==='POST'&&dropNext&&upstream.status===200){dropNext=false;entry.response_dropped=true;report.browser_network.push(entry);save();res.destroy();return;}
      if(req.url==='/chat/v1/consultations'&&req.method==='POST'&&dropNextConsultation&&upstream.status===200){dropNextConsultation=false;entry.response_dropped=true;report.browser_network.push(entry);save();res.destroy();return;}
      if(req.url==='/chat/v1/messages'&&req.method==='POST'&&failNextReceipt&&upstream.status===200){
        failNextReceipt=false;entry.receipt_failure_injected=true;report.browser_network.push(entry);save();
        res.writeHead(503,{'Content-Type':'application/json','Cache-Control':'no-store'}).end(JSON.stringify({error:'unavailable'}));return;
      }
      report.browser_network.push(entry);save();
      for(const name of ['content-type','cache-control','content-security-policy','x-content-type-options','referrer-policy','set-cookie']){const value=upstream.headers.get(name);if(value)res.setHeader(name,value);}
      res.writeHead(upstream.status);res.end(data);
    }catch{res.writeHead(502,{'Content-Type':'application/json'}).end(JSON.stringify({error:'unavailable'}));}
  });
  await new Promise(resolve=>proxy.listen(0,'127.0.0.1',resolve));publicOrigin='http://127.0.0.1:'+proxy.address().port;
  report.browser_origin=publicOrigin;save();
  console.log(JSON.stringify({browser_origin:publicOrigin,report:output+'/report.json',reply_mode:report.reply_mode,mock_slow_ai:mockSlow,commands:['restart-gateway','restart-backend','drop-next','fail-next-receipt','drop-next-consultation',...(ai?['model-down','model-up']:[]),...(mockSlow?['release-model','mock-fast','mock-slow']:[]),'stop']}));
  const lines=readline.createInterface({input:process.stdin});
  for await(const line of lines){
    const command=line.trim();
    if(command==='stop')break;
    if(command==='restart-gateway'){await stop(gateway,'SIGKILL');gateway=await startGateway(gatewayPort);}
    else if(command==='restart-backend'){await stop(host,'SIGKILL');host=await startBackend(hostPort);}
    else if(command==='drop-next')dropNext=true;
    else if(command==='fail-next-receipt')failNextReceipt=true;
    else if(command==='drop-next-consultation')dropNextConsultation=true;
    else if(mockSlow&&command==='release-model'){for(const finish of [...pendingMock])finish();}
    else if(mockSlow&&command==='mock-fast')mockDelayMs=200;
    else if(mockSlow&&command==='mock-slow')mockDelayMs=90000;
    else if(ai&&command==='model-down')modelDown=true;
    else if(ai&&command==='model-up')modelDown=false;
    else {console.log('unknown command');continue;}
    report.checks.push({name:command,performed_at:new Date().toISOString()});save();console.log(command+': ready');
  }
  lines.close();report.finished_at=new Date().toISOString();
}catch{report.passed=false;report.failure_step='browser_lab';process.exitCode=1;}
finally{
  for(const finish of [...pendingMock])finish();
  if(proxy){proxy.closeAllConnections();await new Promise(resolve=>proxy.close(resolve));}
  for(const entry of children.slice().reverse())await stop(entry);
  if(modelProxy){modelProxy.closeAllConnections();await new Promise(resolve=>modelProxy.close(resolve));}
  if(dbStarted){await docker('stop','postgres');report.database_stopped=true;}
  await rm(directory,{recursive:true,force:true});save();console.log('Browser lab stopped; database retained.');
}
