#!/usr/bin/env node
// Disposable PostgreSQL + real Go processes + localhost mock model only.
import fs from 'node:fs/promises';
import {spawn} from 'node:child_process';
import {createServer} from 'node:http';
import {createServer as tcpServer} from 'node:net';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomUUID,randomBytes} from 'node:crypto';
import assert from 'node:assert/strict';
import {cleanEnvironment} from './recovery_check_support.mjs';
const args=process.argv.slice(2);
if(args.includes('--help')){console.log('node scripts/managed_knowledge_e2e.mjs --allow-test-writes [--browser]\nCreates a dedicated PG16 container/volume; migrates only that new DB; uses localhost mock only. --browser waits for UI draft/publish/rollback; cleans everything on exit.');process.exit(0);}
if(!args.includes('--allow-test-writes')||args.some(a=>!['--allow-test-writes','--browser'].includes(a))||new Set(args).size!==args.length){console.error('Explicit --allow-test-writes required; unknown/duplicate option refused.');process.exit(2);}
const browser=args.includes('--browser'),root=fileURLToPath(new URL('../',import.meta.url));
const output=await fs.mkdtemp(join(tmpdir(),'omni-managed-e2e-'));
const id=randomUUID(),container='omni-managed-'+id,volume=container+'-data',label='omni.managed-e2e='+id;
const env=cleanEnvironment(process.env),children=new Set();
const report={status:'running',external_models:false,browser_actions:browser,checks:[],cleanup:[],started_at:new Date().toISOString()};
let interrupted=false,created=false,volumeCreated=false,mock,admin,gateway,cookie='',version='';
const password='fictional-managed-e2e-only-password',account='fixture-admin';
const oldText='虚构验收旧版：服务窗口为09:00至17:00。',newText='虚构验收新版：服务窗口为10:00至18:00。';
const records=[];const delay=ms=>new Promise(r=>setTimeout(r,ms));
async function save(){await fs.writeFile(join(output,'report.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});}
async function run(command,args,extra={},input){
 return await new Promise((resolve,reject)=>{
  const p=spawn(command,args,{cwd:root,env:{...env,...extra},stdio:['pipe','pipe','pipe']});children.add(p);
  let stdout='',stderr='';const timer=setTimeout(()=>p.kill('SIGKILL'),120000);
  p.stdout.on('data',b=>stdout+=b);p.stderr.on('data',b=>stderr+=b);p.stdin.end(input);
  p.on('error',()=>{clearTimeout(timer);children.delete(p);reject(Error('command_spawn_failed'));});
  p.on('close',code=>{clearTimeout(timer);children.delete(p);code===0?resolve(stdout.trim()):reject(Error(command+'_failed'));});
 });
}
const docker=(...args)=>run('docker',['--context','desktop-linux',...args]);
async function freePort(){const server=tcpServer();await new Promise((r,j)=>{server.once('error',j);server.listen(0,'127.0.0.1',r)});const port=server.address().port;await new Promise(r=>server.close(r));return port;}
async function launch(name,extra){
 const log=await fs.open(join(output,name+'-'+randomUUID()+'.log'),'w',0o600);
 const p=spawn(join(output,name),[],{cwd:root,env:{...env,...extra},stdio:['ignore',log.fd,log.fd]});await log.close();children.add(p);p.on('error',()=>{});p.on('close',()=>children.delete(p));return p;
}
async function stop(p){if(!p||p.exitCode!==null||p.signalCode)return;const done=new Promise(r=>p.once('close',r));p.kill('SIGTERM');const timer=setTimeout(()=>p.kill('SIGKILL'),6000);await done;clearTimeout(timer);}
async function waitFor(fn,timeout=30000){const end=Date.now()+timeout;while(Date.now()<end){if(interrupted)throw Error('interrupted');try{const v=await fn();if(v)return v;}catch{}await delay(250);}throw Error('condition_timeout');}
async function http(url,body,headers={}){return fetch(url,{method:body===undefined?'GET':'POST',headers:{...headers,...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(12000),redirect:'error'});}
async function json(url,body,headers={}){const r=await http(url,body,headers);if(!r.ok)throw Error('http_status_'+r.status);return r.json();}
const check=async(name,data={})=>{report.checks.push({name,status:'passed',...data});await save();console.log('PASS '+name);};
const onSignal=()=>{interrupted=true;};process.on('SIGINT',onSignal);process.on('SIGTERM',onSignal);
console.log('Report directory: '+output);
try{
 await docker('info','--format','{{.ServerVersion}}');
 for(const name of ['knowledge-admin','webchat-server','migrate'])await run('go',['build','-o',join(output,name),'./cmd/'+name]);
 volumeCreated=true;await docker('volume','create','--label',label,volume);
 const dbPassword=randomBytes(24).toString('hex');
 created=true;await docker('run','-d','--name',container,'--label',label,'-e','POSTGRES_USER=managed_test','-e','POSTGRES_PASSWORD='+dbPassword,'-e','POSTGRES_DB=managed_e2e_test','-p','127.0.0.1::5432','-v',volume+':/var/lib/postgresql/data','postgres:16-alpine');
 const mapping=await docker('port',container,'5432/tcp');assert.match(mapping,/^127\.0\.0\.1:\d+$/);
 let dsn='postgres://managed_test:'+dbPassword+'@'+mapping+'/managed_e2e_test?sslmode=disable';
 const readyDB=()=>waitFor(async()=>{await docker('exec',container,'pg_isready','-h','127.0.0.1','-U','managed_test','-d','managed_e2e_test');return true;});await readyDB();
 await run(join(output,'migrate'),[],{DATABASE_URL:dsn});
 const hash=await run(join(output,'knowledge-admin'),['-hash-password'],{},password+'\n');
 const accountFile=join(output,'accounts.json');await fs.writeFile(accountFile,JSON.stringify([{id:account,password_hash:hash,permissions:['knowledge.read','knowledge.edit','knowledge.publish']}]),{mode:0o600});
 const adminPort=await freePort(),adminOrigin='http://127.0.0.1:'+adminPort;
 const adminEnv={DATABASE_URL:dsn,KNOWLEDGE_ADMIN_ACCOUNTS_FILE:accountFile,KNOWLEDGE_ADMIN_ADDRESS:'127.0.0.1:'+adminPort,KNOWLEDGE_ADMIN_ORIGIN:adminOrigin};
 const startAdmin=async()=>{admin=await launch('knowledge-admin',adminEnv);await waitFor(async()=> (await http(adminOrigin+'/')).status===200);};
 const login=async()=>{const r=await http(adminOrigin+'/auth/login',{account,password},{Origin:adminOrigin});assert.equal(r.status,200);cookie=r.headers.get('set-cookie').split(';')[0];const me=await json(adminOrigin+'/auth/me',undefined,{Cookie:cookie});version=me.version;};
 const call=(path,body)=>json(adminOrigin+path,body,{Origin:adminOrigin,Cookie:cookie,'X-Knowledge-Session':version});
 const state=()=>call('/internal/knowledge/state');
 const draft=(revision,content)=>call('/internal/knowledge/draft',{revision,entries:[{id:'service-hours',title:'服务时间',source:'虚构测试手册',content,keywords:['服务时间']}]});
 await startAdmin();await login();await draft(0,oldText);await call('/internal/knowledge/publish',{revision:1});const seeded=await state(),oldVersion=seeded.active;
 await check('initial_publication',{revision:seeded.revision,active:oldVersion});
 mock=createServer(async(req,res)=>{try{
  assert.equal(req.url,'/v1/chat/completions');assert.equal(req.method,'POST');assert.equal(req.headers.authorization,'Bearer local-mock-only');
  let raw='';for await(const chunk of req){raw+=chunk;if(raw.length>2**20)throw Error('oversized');}
  const input=JSON.parse(raw);assert.equal(input.model,'mock');
  const text=m=>typeof m.content==='string'?m.content:(m.content||[]).map(p=>p.text||'').join('');
  const prefix='BEGIN_UNTRUSTED_KNOWLEDGE_JSON\n',suffix='\nEND_UNTRUSTED_KNOWLEDGE_JSON';
  const envelopes=input.messages.filter(m=>text(m).startsWith(prefix));assert.equal(envelopes.length,1);assert.equal(envelopes[0].role,'user');
  const references=JSON.parse(text(envelopes[0]).slice(prefix.length,-suffix.length));assert.equal(references.snippets.length,1);
  const content=references.snippets[0].content;assert.ok([oldText,newText].includes(content));records.push(content);
  res.setHeader('Content-Type','application/json');res.end(JSON.stringify({id:'local-'+records.length,object:'chat.completion',created:1,model:'mock',choices:[{index:0,message:{role:'assistant',content},finish_reason:'stop'}],usage:{prompt_tokens:1,completion_tokens:1,total_tokens:2}}));
 }catch{res.statusCode=500;res.end('invalid_local_fixture_request');}});
 await new Promise(r=>mock.listen(0,'127.0.0.1',r));
 const gatewayPort=await freePort(),gatewayOrigin='http://127.0.0.1:'+gatewayPort,hostSecret=randomBytes(32).toString('hex');
 const gatewayEnv={DATABASE_URL:dsn,WEBCHAT_ADDRESS:'127.0.0.1:'+gatewayPort,WEBCHAT_HOST_ID:'managed-e2e',WEBCHAT_HOST_SECRET:hostSecret,WEBCHAT_REPLY_MODE:'ai',WEBCHAT_KNOWLEDGE_SOURCE:'managed',AI_PROVIDER:'custom',AI_PROTOCOL:'chat_completions',AI_MODEL:'mock',AI_BASE_URL:'http://127.0.0.1:'+mock.address().port+'/v1',AI_API_KEY:'local-mock-only'};
 const startGateway=async()=>{gateway=await launch('webchat-server',gatewayEnv);await waitFor(async()=> (await http(gatewayOrigin+'/internal/knowledge/state')).status===404);};await startGateway();
 const ask=async(name,want)=>{
  const session=await json(gatewayOrigin+'/internal/chat/sessions',{customer_id:'fixture-'+name},{Authorization:'Bearer '+hostSecret});const headers={Authorization:'Bearer '+session.access_token};
  const before=records.length;const result=await json(gatewayOrigin+'/chat/v1/messages',{event_id:randomUUID(),content:'服务时间'},headers);assert.equal(result.outcome,'replied');assert.equal(records.length,before+1);assert.equal(records.at(-1),want);
  const history=await json(gatewayOrigin+'/chat/v1/messages',undefined,headers);assert.equal(history.messages.find(m=>m.id===result.reply_message_id)?.content,want);
  await check(name,{reference:want,reply_persisted:true,mock_requests:records.length});
 };
 await ask('initial_uses_old',oldText);
 if(browser){console.log('BROWSER '+adminOrigin+' account '+account+' password '+password);console.log('UI STEP: edit answer exactly to '+newText+' ; save draft, then publish, then rollback to '+oldVersion);}
 const awaitRevision=async n=>{const s=await waitFor(async()=>{const v=await state();return v.revision>=n?v:null},600000);assert.equal(s.revision,n);return s;};
 if(!browser)await draft(2,newText);const draftState=await awaitRevision(3);assert.equal(draftState.active,oldVersion);assert.equal(draftState.draft.entries[0].content,newText);await ask('draft_still_uses_old',oldText);
 if(!browser)await call('/internal/knowledge/publish',{revision:3});const published=await awaitRevision(4);assert.notEqual(published.active,oldVersion);await ask('publication_uses_new',newText);
 if(!browser)await call('/internal/knowledge/rollback',{revision:4,version:oldVersion});const rolled=await awaitRevision(5);assert.equal(rolled.active,oldVersion);assert.equal(rolled.draft.version,published.draft.version);await ask('rollback_uses_old',oldText);
 await stop(gateway);await stop(admin);await docker('restart',container);await readyDB();
 // Docker may assign a new ephemeral host port after restart. Re-resolve it
 // instead of mistaking a stale DSN for lost persisted knowledge.
 const restartedMapping=await docker('port',container,'5432/tcp');assert.match(restartedMapping,/^127\.0\.0\.1:\d+$/);
 report.database_host_port_changed_on_restart=restartedMapping!==mapping;
 dsn='postgres://managed_test:'+dbPassword+'@'+restartedMapping+'/managed_e2e_test?sslmode=disable';
 adminEnv.DATABASE_URL=dsn;gatewayEnv.DATABASE_URL=dsn;
 await waitFor(async()=>{await run(join(output,'webchat-server'),['-check-resources'],gatewayEnv);return true;});
 await startAdmin();assert.equal((await http(adminOrigin+'/auth/me',undefined,{Cookie:cookie})).status,401);await login();assert.deepEqual(await state(),rolled);
 await startGateway();await ask('after_process_and_database_restart_uses_old',oldText);await check('restart_preserves_exact_state_and_revokes_admin_session');
 report.status='passed';
}catch(e){report.status='failed';report.failure=e.code||e.message?.replace(/postgres:\/\/[^\s]+/g,'[redacted]')||'unknown_failure';console.error('FAIL; see private report');process.exitCode=1;}
finally{
 for(const child of [...children])await stop(child);
 if(mock){mock.closeAllConnections();await new Promise(r=>mock.close(r));}
 for(const [kind,name,enabled] of [['container',container,created],['volume',volume,volumeCreated]])if(enabled){try{
  const found=await docker(kind==='container'?'inspect':'volume',...(kind==='container'?[]:['inspect']),'--format','{{ index .'+(kind==='container'?'Config.Labels':'Labels')+' "omni.managed-e2e" }}',name);
  assert.equal(found,id);await docker(...(kind==='container'?['rm','-f',name]:['volume','rm',name]));report.cleanup.push({kind,status:'removed'});
 }catch{report.cleanup.push({kind,status:'cleanup_failed',name});report.status='failed';process.exitCode=1;}}
 await fs.rm(join(output,'accounts.json'),{force:true});await save();console.log(report.status.toUpperCase()+' '+join(output,'report.json'));
}
