import {rotationLab} from './rotation.mjs';
import {createInterface} from 'node:readline';
import {execFile as exec} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp,mkdir,writeFile,readFile,copyFile,rm,open,rename} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {resolve,join,dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes,X509Certificate,randomUUID} from 'node:crypto';
import {verifyHost} from '../../integrations/host-kit/verify.mjs';
const run=promisify(exec),root=fileURLToPath(new URL('../../',import.meta.url));
const args=process.argv.slice(2);
const rotationMode=args.includes('--rotation');if(rotationMode)args.splice(args.indexOf('--rotation'),1);
const browserMode=args.at(-1)==='--browser';if(browserMode)args.pop();
if(browserMode&&!process.stdin.isTTY)throw Error('browser_mode_requires_interactive_terminal');
if(args.length!==3||args[0]!=='--allow-isolated-test-writes'||args[1]!=='--report')throw Error('usage: --allow-isolated-test-writes --report NEW_FILE [--browser] [--rotation]');
const reportFile=await open(resolve(args[2]),'wx',0o600);
const report={started_at:new Date().toISOString(),passed:false,external_model_calls:0,browser_verified:false,support_may_open:false,checks:[]};
const prefix='omni-private-'+randomBytes(5).toString('hex'),owned=[],nets=[];
const dir=await mkdtemp(join(tmpdir(),prefix+'-')),secret=()=>randomBytes(32).toString('base64url');
const docker=(...a)=>run('docker',a,{maxBuffer:4*1024*1024,timeout:180000});
const ensure=(ok,label)=>{if(!ok){report.failed_check=label;throw Error(label);}report.checks.push(label);};
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function wait(fn){for(let n=0;n<100;n++){try{if(await fn())return;}catch{}await sleep(300);}throw Error('readiness_timeout');}
async function container(name,...a){owned.push(name);await docker('run','-d','--name',name,...a);}
let stage='setup',image=prefix+':local',imageBuilt=false;
try{
 for(const sub of ['business','transport','ca','build','build/bin'])await mkdir(join(dir,sub),{mode:0o700});
 const ssl=(...a)=>run('openssl',a,{cwd:dir,timeout:30000});
 await ssl('req','-x509','-newkey','rsa:2048','-nodes','-keyout','ca/key','-out','ca/cert','-days','1','-subj','/CN=isolated-test-ca');
 for(const [name,eku] of [['server','serverAuth'],['client','clientAuth']]){
  await ssl('req','-newkey','rsa:2048','-nodes','-keyout',name+'.key','-out',name+'.csr','-subj','/CN='+name);
  await writeFile(join(dir,name+'.ext'),'extendedKeyUsage='+eku+'\n'+(name==='server'?'subjectAltName=DNS:gateway\n':''));
  await ssl('x509','-req','-in',name+'.csr','-CA','ca/cert','-CAkey','ca/key','-CAcreateserial','-out',name+'.crt','-days','1','-extfile',name+'.ext');
  const dest=name==='server'?'transport':'business';
  for(const ext of ['key','crt'])await copyFile(join(dir,name+'.'+ext),join(dir,dest,name+'.'+ext));
  await copyFile(join(dir,'ca/cert'),join(dir,dest,'ca.crt'));
 }
 const fingerprint=new X509Certificate(await readFile(join(dir,'client.crt'))).fingerprint256;
 await writeFile(join(dir,'transport/settings.json'),JSON.stringify({clientFingerprint:fingerprint}),{mode:0o600});
 const password=secret(),hostSecret=secret(),agentSecret=secret();
 const accounts=[{realm:'member',handle:'lin',number:'MEM:731',passphrase:password},{realm:'member',handle:'zhou',number:'MEM:864',passphrase:password},{realm:'operator',handle:'staff',number:'OPS:42',passphrase:password}];
 const config={serveUI:browserMode,accounts,hostSecret,agents:[{id:'staff-1',name:'Test staff',secret:agentSecret}]};
 await writeFile(join(dir,'business/settings.json'),JSON.stringify(config),{mode:0o600});
 stage='build_current_gateway';console.log(stage);
 const arch=(await docker('info','--format','{{.Architecture}}')).stdout.trim();
 const GOARCH=({aarch64:'arm64',arm64:'arm64',x86_64:'amd64',amd64:'amd64'})[arch];if(!GOARCH)throw Error('unsupported_arch');
 for(const name of ['webchat-server','migrate'])await run('go',['build','-o',join(dir,'build/bin',name),'./cmd/'+name],{cwd:root,env:{...process.env,GOOS:'linux',GOARCH,CGO_ENABLED:'0'},timeout:180000,maxBuffer:1048576});
 await copyFile(join(root,'deploy/crmeb/Dockerfile'),join(dir,'build/Dockerfile'));
 await docker('build','-t',image,join(dir,'build'));imageBuilt=true;
 for(const [name,internal] of [[prefix+'-private',true],[prefix+'-edge',false]]){await docker('network','create',...(internal?['--internal']:[]),name);nets.push(name);}
 const pg=prefix+'-pg',gateway=prefix+'-gateway',transport=prefix+'-tls',business=prefix+'-business',dbpass=secret();
 await writeFile(join(dir,'pg.env'),'POSTGRES_USER=lab\nPOSTGRES_DB=lab\nPOSTGRES_PASSWORD='+dbpass+'\n',{mode:0o600});
 await writeFile(join(dir,'gateway.env'),['DATABASE_URL=postgres://lab:'+dbpass+'@'+pg+':5432/lab?sslmode=disable','WEBCHAT_HOST_ID='+prefix,'WEBCHAT_HOST_SECRET='+hostSecret,'WEBCHAT_REPLY_MODE=fake','WEBCHAT_ADDRESS=127.0.0.1:8093','WEBCHAT_AGENTS_JSON='+JSON.stringify([{source_id:prefix,id:'staff-1',secret:agentSecret}])].join('\n'),{mode:0o600});
 stage='start_isolated_services';console.log(stage);
 await container(pg,'--network',nets[0],'--env-file',join(dir,'pg.env'),'--tmpfs','/var/lib/postgresql/data','postgres:16-alpine');
 await wait(async()=>{await docker('exec',pg,'pg_isready','-h','127.0.0.1','-U','lab','-d','lab');return true;});
 await docker('run','--rm','--network',nets[0],'--env-file',join(dir,'gateway.env'),'--entrypoint','/migrate',image);
 await container(gateway,'--network',nets[0],'--network-alias','gateway','--env-file',join(dir,'gateway.env'),image);
 // Copy only runtime modules: never mount repository .env, reports or Git data.
 const modules=['integrations/fresh-onboarding/business.mjs','integrations/fresh-onboarding/support.mjs','integrations/office-portal/legacy.mjs',...['adapter','identity.example','onboarding','proxy'].map(n=>'integrations/host-kit/'+n+'.mjs'),...['browser-gate','transport','edge','reloadable','business-worker','gateway-worker','probe'].map(n=>'deploy/private-containers/'+n+'.mjs')];
 if(browserMode)for(const name of ['index.html','app.js','app.css'])modules.push('integrations/fresh-onboarding/public/'+name);
 for(const path of modules){await mkdir(dirname(join(dir,'source',path)),{recursive:true});await copyFile(join(root,path),join(dir,'source',path));}
 const nodeMount=['--read-only','--cap-drop','ALL','--security-opt','no-new-privileges','-v',join(dir,'source')+':/src:ro'];
 await container(transport,'--network','container:'+gateway,...nodeMount,'-v',join(dir,'transport')+':/run/transport:ro','node:24-alpine','node','/src/deploy/private-containers/gateway-worker.mjs');
 await container(business,'--network',nets[1],'-p','127.0.0.1::8080',...nodeMount,'-v',join(dir,'business')+':/run/business:ro','node:24-alpine','node','/src/deploy/private-containers/business-worker.mjs');
 await docker('network','connect',nets[0],business);
 const binding=(await docker('port',business,'8080/tcp')).stdout.trim();ensure(/^127\.0\.0\.1:\d+$/.test(binding),'public_loopback_only');
 config.origin='http://'+binding;
 await writeFile(join(dir,'business/settings.next'),JSON.stringify(config),{mode:0o600});
 await rename(join(dir,'business/settings.next'),join(dir,'business/settings.json'));
 for(const n of [gateway,transport,pg]){const info=JSON.parse((await docker('inspect',n)).stdout)[0];ensure(!Object.values(info.NetworkSettings.Ports||{}).some(x=>x?.length),'no_host_port_'+n.split('-').at(-1));}
 let cookie='';
 async function call(path,body,token){const r=await fetch(config.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:config.origin,...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{}),...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(15000),redirect:'error'});const cookies=r.headers.getSetCookie();if(cookies.length)cookie=cookies.map(x=>x.split(';')[0]).join('; ');return {status:r.status,data:await r.json().catch(()=>null)};}
 await wait(async()=>(await call('/host/me')).status===200);
 stage='full_business_acceptance';console.log(stage);
 report.business=await verifyHost({origin:config.origin,customers:[{account:'lin',password},{account:'zhou',password}],staff:{account:'staff',password},paths:{customerLogin:'/auth/customer/login',customerLogout:'/auth/customer/logout',staffLogin:'/auth/staff/login',staffLogout:'/auth/staff/logout'}},{allowTestWrites:true});
 ensure(report.business.passed,'business_lifecycle');
 ensure((await call('/internal/chat/sessions',{},hostSecret)).status===404,'public_internal_denied');
 ensure((await call('/demo/login',{})).status===404,'demo_login_denied');
 ensure((await call('/host/chat-session',{})).status===401,'anonymous_session_denied');
 stage='transport_authentication';console.log(stage);
 for(const mode of ['no-certificate','wrong-key','raw-port']){const p=JSON.parse((await docker('exec',business,'node','/src/deploy/private-containers/probe.mjs',mode)).stdout);ensure(mode==='wrong-key'?p.status===401:p.denied===true,mode+'_denied');}
 await call('/auth/customer/login',{account:'lin',password});
 let s=await call('/host/chat-session',{});ensure(s.status===200,'restart_session');let token=s.data.access_token;const conversation=s.data.conversation_id;
 const event_id=randomUUID(),content='private transport recovery '+event_id,sent=await call('/chat/v1/messages',{event_id,content},token);ensure(sent.status===200,'restart_marker_saved');
 stage='disconnect_recovery';console.log(stage);
 await docker('stop','-t','2',transport);
 ensure([502,503,504].includes((await call('/chat/v1/messages',undefined,token)).status),'disconnect_fails_closed');
 await docker('start',transport);
 await wait(async()=>(await call('/chat/v1/messages',undefined,token)).status===200);
 ensure((await call('/chat/v1/messages',undefined,token)).data.messages.filter(x=>x.content===content).length===1,'disconnect_history_recovered');
 stage='gateway_restart';console.log(stage);
 await docker('stop','-t','2',transport);await docker('restart','-t','2',gateway);await docker('start',transport);
 await wait(async()=>{const p=JSON.parse((await docker('exec',business,'node','/src/deploy/private-containers/probe.mjs','wrong-key')).stdout);return p.status===401;});
 ensure((await call('/chat/v1/messages',undefined,token)).status===401,'restart_old_capability_denied');
 s=await call('/host/chat-session',{});ensure(s.status===200&&s.data.conversation_id===conversation,'restart_binding_preserved');token=s.data.access_token;
 const history=await call('/chat/v1/messages',undefined,token);ensure(history.status===200&&history.data.messages.filter(x=>x.content===content).length===1,'restart_history_preserved');
 const receipt=await call('/chat/v1/message-receipt',{event_id,content},token);ensure(receipt.status===200&&receipt.data.accepted&&receipt.data.message_id===sent.data.message_id,'restart_receipt_preserved');
 const rotation=rotationLab({dir,config,docker,ssl,gateway,transport,business,image,network:nets[0],nodeMount,ensure,wait});
 if(rotationMode&&!browserMode){
  stage='rotation';await rotation.stage();await rotation.rejectInvalid();
  const pending={event_id:randomUUID(),content:'ROTATION pending message'};
  ensure([401,403,502,503].includes((await call('/chat/v1/messages',pending,token)).status),'rotation_mismatch_fails_closed');
  await rotation.activate();await rotation.verify();
  s=await call('/host/chat-session',{});ensure(s.status===200&&s.data.conversation_id===conversation,'rotation_business_login_preserved');token=s.data.access_token;
  const sentPending=await call('/chat/v1/messages',pending,token);ensure(sentPending.status===200,'rotation_pending_retry_accepted');
  ensure((await call('/chat/v1/messages',pending,token)).status===200,'rotation_pending_identical_retry');
  const restored=await call('/chat/v1/messages',undefined,token);ensure(restored.status===200&&restored.data.messages.filter(m=>m.content===pending.content).length===1&&restored.data.messages.filter(m=>m.content===content).length===1,'rotation_history_and_deduplication');
  const rr=await call('/chat/v1/message-receipt',pending,token);ensure(rr.status===200&&rr.data.accepted&&rr.data.message_id===sentPending.data.message_id,'rotation_receipt_consistent');
 }
 if(browserMode){
  stage='browser_lab';
  console.log(JSON.stringify({browser_lab_ready:true,origin:config.origin,accounts:accounts.map(a=>({account:a.handle,password:a.passphrase,role:a.realm})),commands:'hold-renewal | hold-send | release | gate-audit | disconnect | reconnect | restart-gateway | rotate-stage | rotate-bad-config | rotate-activate | rotate-verify | audit | records | stop'}));
  const input=createInterface({input:process.stdin});
  const interrupt=()=>input.close();for(const signal of ['SIGINT','SIGTERM'])process.once(signal,interrupt);
  try{for await(const line of input){
   const command=line.trim();if(command==='stop')break;
   if(['hold-renewal','hold-send','release'].includes(command))await writeFile(join(dir,'business/browser-gate'),command==='hold-renewal'?'renewal':command==='hold-send'?'send':'');
   else if(command==='gate-audit'){const logs=(await docker('logs',business)).stdout;console.log(logs.split('\n').filter(x=>x.startsWith('{')&&x.includes('browser_gate')).join('\n'));}
   else if(command==='rotate-stage')await rotation.stage();
   else if(command==='rotate-bad-config')await rotation.rejectInvalid();
   else if(command==='rotate-activate')await rotation.activate();
   else if(command==='rotate-verify')await rotation.verify();
   else if(command==='disconnect')await docker('stop','-t','2',transport);
   else if(command==='reconnect')await docker('start',transport);
   else if(command==='restart-gateway'){await docker('stop','-t','2',transport);await docker('restart','-t','2',gateway);await docker('start',transport);}
   else if(command==='audit'){const logs=(await docker('logs',business)).stdout;const entries=logs.split('\n').filter(x=>x.startsWith('{')).map(x=>JSON.parse(x)).filter(x=>x.edge_request);console.log(JSON.stringify({requests:entries}));}
   else if(command==='records'){const sql="SELECT json_build_object('sender',sender,'content',content,'conversation_id',conversation_id,'id',id) FROM customer_service.messages WHERE content LIKE 'BROWSER-%' ORDER BY created_at,id";console.log((await docker('exec',pg,'psql','-U','lab','-d','lab','-At','-c',sql)).stdout);}
   else {console.log('unknown command');continue;}
   console.log(JSON.stringify({command,completed:true}));
  }}finally{input.close();process.stdin.pause();for(const signal of ['SIGINT','SIGTERM'])process.removeListener(signal,interrupt);}
  report.browser_lab_opened=true; // UI acceptance must be recorded separately; opening is not verification.
 }
 report.passed=true;
}catch{report.failed_stage=stage;process.exitCode=1;}
finally{
 let failures=0;for(const name of owned.reverse())try{await docker('rm','-f',name);}catch{failures++;}
 for(const name of nets.reverse())try{await docker('network','rm',name);}catch{failures++;}
 if(imageBuilt)try{await docker('image','rm',image);}catch{failures++;}
 await rm(dir,{recursive:true,force:true});report.cleanup_passed=failures===0;if(failures){report.passed=false;process.exitCode=1;}
 report.finished_at=new Date().toISOString();await reportFile.writeFile(JSON.stringify(report,null,2)+'\n');await reportFile.close();console.log(JSON.stringify(report));
}
