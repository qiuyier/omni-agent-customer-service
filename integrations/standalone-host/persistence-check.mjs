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
const root=fileURLToPath(new URL('../../',import.meta.url));
if(process.argv.length!==3||process.argv[2]!=='--allow-local-postgres')throw Error('Requires --allow-local-postgres; creates dedicated local test database/volume');
const compose='deploy/standalone-host/compose.yaml';
const dsn='postgres://standalone_demo:standalone_local_test@127.0.0.1:15434/standalone_chat_acceptance?sslmode=disable';
const runID=new Date().toISOString().replace(/[:.]/g,'-')+'-'+randomBytes(4).toString('hex');
const directory=await mkdtemp(join(tmpdir(),'omni-standalone-pg-'));
const output=root+'examples/archive/integration/standalone-host/'+runID;
fs.mkdirSync(output,{recursive:true});
const report={started_at:new Date().toISOString(),passed:false,storage:'postgres',reply_mode:'fake',external_model_calls:0,source_id:'standalone-check-'+runID,checks:[],processes:[],database_retained:true};
const save=()=>fs.writeFileSync(output+'/report.json',JSON.stringify(report,null,2)+'\n',{mode:0o600});
function ensure(ok,name){report.checks.push({name,passed:!!ok});save();if(!ok)throw Error(name);}
const run=promisify(execFile);
const env={PATH:process.env.PATH};
const docker=(...args)=>run('docker',['--context','desktop-linux','compose','--env-file','/dev/null','-f',compose,...args],{cwd:root,env:{...env,HOME:process.env.HOME},timeout:120000,maxBuffer:1024*1024});
const secret=randomBytes(32).toString('base64url');
const agents=['staff-1','staff-2'].map(id=>({id,name:id,secret:randomBytes(32).toString('base64url')}));
let gateway,host,proxy,dbStarted=false,activeStep='setup';
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
  const entry=await launch(join(directory,'gateway'),['-address','127.0.0.1:'+port],{...env,DATABASE_URL:dsn,WEBCHAT_HOST_ID:report.source_id,WEBCHAT_HOST_SECRET:secret,WEBCHAT_REPLY_MODE:'fake',WEBCHAT_AGENTS_JSON:JSON.stringify(agents.map(({id,secret})=>({id,secret,source_id:report.source_id})))},/Web chat server: (http:\/\/127\.0\.0\.1:\d+)\n/,'gateway');
  const ready=await fetch(entry.origin+'/readyz',{signal:AbortSignal.timeout(5000)});ensure(ready.status===200,'gateway_ready');await ready.body?.cancel();return entry;
};
const startBackend=(port=0)=>launch(process.execPath,['integrations/standalone-host/host-worker.mjs'],{...env,STANDALONE_GATEWAY_ORIGIN:gateway.origin,STANDALONE_HOST_SECRET:secret,STANDALONE_AGENTS_JSON:JSON.stringify(agents),STANDALONE_PUBLIC_PORT:String(port)},/^(http:\/\/127\.0\.0\.1:\d+)\n/,'business-backend');
const request=async(path,{method='GET',body,cookie,token,base=host.origin}={})=>{
  const r=await fetch(base+path,{method,headers:{...(method==='POST'?{Origin:host.origin,'Content-Type':'application/json'}:{}),...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(25000)});
  const text=await r.text();return{status:r.status,data:text?JSON.parse(text):null,cookie:r.headers.get('set-cookie')?.split(';')[0]};
};
async function login(account){const r=await request('/demo/login',{method:'POST',body:{account}});ensure(r.status===200&&r.cookie,'login_'+account);return r.cookie;}
async function session(cookie){const r=await request('/host/chat-session',{method:'POST',body:{},cookie});ensure(r.status===200&&r.data.storage==='postgres'&&r.data.reply_mode==='fake','persistent_session_metadata');return r.data;}
async function history(cookie,s){const r=await request('/chat/v1/messages',{cookie,token:s.access_token});ensure(r.status===200,'history_read');return r.data;}
try{
  save();
  activeStep='database_start';
  ensure(!(await docker('ps','--status','running','-q','postgres')).stdout.trim(),'dedicated_database_not_already_in_use');
  dbStarted=true;await docker('up','-d','--wait','postgres');
  activeStep='build';await run('go',['build','-o',join(directory,'migrate'),'./cmd/migrate'],{cwd:root,timeout:120000});await run('go',['build','-o',join(directory,'gateway'),'./cmd/webchat-server'],{cwd:root,timeout:120000});
  activeStep='migration';await run(join(directory,'migrate'),[],{cwd:root,env:{...env,DATABASE_URL:dsn},timeout:30000});
  activeStep='initial_login';gateway=await startGateway();host=await startBackend();
  const gatewayPort=Number(new URL(gateway.origin).port),hostPort=Number(new URL(host.origin).port);
  let a=await login('lin'),b=await login('zhou'),sa=await session(a),sb=await session(b);
  const originalConversation=sa.conversation_id,otherConversation=sb.conversation_id;
  ensure(originalConversation!==otherConversation,'distinct_accounts');
  ensure((await history(a,sa)).messages.length===0&&(await history(b,sb)).messages.length===0,'new_run_empty_history');
  // Test-client journal represents the same immutable event retained across
  // network failure and service restarts. No credentials are saved here.
  const pending={event_id:'durable-event-1',content:'虚构账号甲：响应丢失后请按原事件恢复。'};
  fs.writeFileSync(output+'/pending-event.json',JSON.stringify(pending,null,2)+'\n');
  let observedLost;
  proxy=http.createServer(async(req,res)=>{
    try{
      const body=[];for await(const chunk of req)body.push(chunk);
      if(req.method!=='POST'||req.url!=='/chat/v1/messages'){res.writeHead(404).end();return;}
      const upstream=await fetch(host.origin+req.url,{method:'POST',headers:{Origin:host.origin,'Content-Type':'application/json',Cookie:req.headers.cookie,Authorization:req.headers.authorization},body:Buffer.concat(body),redirect:'error',signal:AbortSignal.timeout(20000)});
      const text=await upstream.text();
      if(upstream.status===200){observedLost=JSON.parse(text);res.destroy();}
      else {res.writeHead(502).end();}
    }catch{res.destroy();}
  });
  await new Promise(resolve=>proxy.listen(0,'127.0.0.1',resolve));
  activeStep='lost_response';let lost=false;
  try{await request('/chat/v1/messages',{method:'POST',body:pending,cookie:a,token:sa.access_token,base:`http://127.0.0.1:${proxy.address().port}`});}catch{lost=true;}
  ensure(lost&&observedLost?.outcome==='replied','success_response_destroyed_before_client_received_it');
  report.lost_response_ids={customer:observedLost.message_id,reply:observedLost.reply_message_id};save();
  proxy.closeAllConnections();await new Promise(resolve=>proxy.close(resolve));proxy=null;
  activeStep='gateway_crash_restart';await stop(gateway,'SIGKILL');gateway=await startGateway(gatewayPort);
  ensure((await request('/chat/v1/messages',{cookie:a,token:sa.access_token})).status===401,'gateway_restart_rejects_old_capability');
  const oldCapability=sa.access_token;sa=await session(a);
  ensure(sa.access_token!==oldCapability&&sa.conversation_id===originalConversation,'renew_capability_same_conversation');
  const replay=(await request('/chat/v1/messages',{method:'POST',body:JSON.parse(fs.readFileSync(output+'/pending-event.json')),cookie:a,token:sa.access_token}));
  ensure(replay.status===200&&JSON.stringify(replay.data)===JSON.stringify(observedLost),'original_event_replays_same_authoritative_ids');
  const ah=await history(a,sa);ensure(ah.messages.length===2&&ah.messages[0].id===observedLost.message_id&&ah.messages[1].id===observedLost.reply_message_id,'no_duplicate_saved_reply_after_restart');
  ensure((await request('/chat/v1/messages',{cookie:b,token:sb.access_token})).status===401,'second_old_capability_invalid');sb=await session(b);
  ensure((await history(b,sb)).messages.length===0,'other_account_still_empty');
  const br=await request('/chat/v1/messages',{method:'POST',body:{event_id:pending.event_id,content:'虚构账号乙：同名事件只属于自己的会话。'},cookie:b,token:sb.access_token});
  ensure(br.status===200&&br.data.message_id!==observedLost.message_id,'same_event_id_isolated_across_accounts');
  const bh=await history(b,sb);ensure(bh.messages.length===2&&!JSON.stringify(bh).includes(pending.content),'other_history_isolated');
  activeStep='backend_crash_restart';const oldCookie=a,oldHostCap=sa.access_token;await stop(host,'SIGKILL');host=await startBackend(hostPort);
  ensure((await request('/host/chat-session',{method:'POST',body:{},cookie:oldCookie})).status===401,'backend_restart_invalidates_login');
  a=await login('lin');b=await login('zhou');sa=await session(a);sb=await session(b);
  ensure(sa.conversation_id===originalConversation&&sb.conversation_id===otherConversation,'backend_relogin_preserves_identity_mapping');
  ensure((await request('/chat/v1/messages',{cookie:a,token:oldHostCap})).status===401,'new_login_rejects_old_host_bound_capability');
  ensure(JSON.stringify((await history(a,sa)).messages)===JSON.stringify(ah.messages)&&JSON.stringify((await history(b,sb)).messages)===JSON.stringify(bh.messages),'both_histories_unchanged_after_backend_restart');
  ensure((await request('/chat/v1/messages',{cookie:b,token:sa.access_token})).status===401,'cross_account_token_rejected_after_restarts');
  activeStep='unavailable_gateway';await stop(gateway);
  const outageEvent={event_id:'outage-event-2',content:'虚构账号甲：服务恢复后重试此事件。'};
  const unavailable=await request('/chat/v1/messages',{method:'POST',body:outageEvent,cookie:a,token:sa.access_token});ensure(unavailable.status===502,'gateway_outage_is_not_success');
  gateway=await startGateway(gatewayPort);
  ensure((await request('/chat/v1/messages',{cookie:a,token:sa.access_token})).status===401,'recovered_gateway_requires_new_capability');sa=await session(a);
  const recovered=await request('/chat/v1/messages',{method:'POST',body:outageEvent,cookie:a,token:sa.access_token});ensure(recovered.status===200&&recovered.data.outcome==='replied','same_outage_event_recovers');
  ensure((await history(a,sa)).messages.length===4,'outage_retry_adds_exactly_one_pair');
  // Preserve service state, not just message rows, across real process crashes.
  activeStep='human_waiting_restart';
  const staffLogin=async account=>{const r=await request('/desk/login',{method:'POST',body:{account}});ensure(r.status===200&&r.cookie,'staff_login_'+account);return r.cookie;};
  let staff=await staffLogin('staff-1'),otherStaff=await staffLogin('staff-2');
  const action=(cookie,body)=>request('/desk/action',{method:'POST',cookie,body:{conversation_id:originalConversation,expected_agent_id:cookie===staff?'staff-1':'staff-2',...body}});
  const deskHistory=async()=>{const r=await action(staff,{action:'history'});ensure(r.status===200,'staff_history_read');return r.data;};
  const queue=async cookie=>{const r=await request('/desk/queue',{cookie});ensure(r.status===200,'staff_queue_read');return r.data.items;};
  const stateKey=data=>JSON.stringify({status:data.status,revision:data.revision,unread_count:data.unread_count,unread_message_ids:data.unread_message_ids||[],messages:data.messages||[]});
  const handoff=await request('/chat/v1/handoff',{method:'POST',body:{},cookie:a,token:sa.access_token});
  ensure(handoff.status===200&&handoff.data.status==='waiting_human','persistent_handoff');
  const waiting=await deskHistory();
  ensure(waiting.unread_count===2,'waiting_has_two_unread_customers');
  await stop(gateway,'SIGKILL');gateway=await startGateway(gatewayPort);
  ensure(stateKey(await deskHistory())===stateKey(waiting),'waiting_state_and_unread_survive_gateway_restart');
  ensure((await queue(otherStaff)).some(row=>row.conversation_id===originalConversation),'waiting_visible_to_other_staff');
  const claim=await action(staff,{action:'claim',revision:waiting.revision});
  ensure(claim.status===200,'claim_after_restart');
  const read=await action(staff,{action:'read',message_ids:[observedLost.message_id]});
  ensure(read.status===200,'mark_one_customer_read');
  const replyEvent={action:'reply',event_id:'durable-human-reply-1',content:'虚构坐席：重启后重试同一回复。'};
  fs.writeFileSync(output+'/pending-human-event.json',JSON.stringify(replyEvent,null,2)+'\n');
  const humanReply=await action(staff,replyEvent);
  ensure(humanReply.status===200,'human_reply_before_restart');
  const active=await deskHistory();
  ensure(active.status==='human_active'&&active.unread_count===1&&active.unread_message_ids?.length===1&&!active.unread_message_ids.includes(observedLost.message_id),'one_read_one_unread_before_restart');
  const ownership=async label=>{
    ensure((await queue(staff)).some(row=>row.conversation_id===originalConversation&&row.status==='human_active'),label+'_owner_queue');
    ensure(!(await queue(otherStaff)).some(row=>row.conversation_id===originalConversation),label+'_other_queue_excluded');
    ensure((await action(otherStaff,{action:'history'})).status===403,label+'_other_staff_history_denied');
    for(const operation of ['reply','close'])ensure((await action(otherStaff,{action:operation,event_id:'unauthorized-reply',content:'must reject',revision:active.revision})).status===403,label+'_other_staff_'+operation+'_denied');
  };
  activeStep='human_active_gateway_restart';
  await stop(gateway,'SIGKILL');gateway=await startGateway(gatewayPort);
  ensure(stateKey(await deskHistory())===stateKey(active),'claimed_history_revision_and_attention_survive_gateway_restart');
  await ownership('gateway_restart');
  const replayHuman=await action(staff,JSON.parse(fs.readFileSync(output+'/pending-human-event.json')));
  ensure(replayHuman.status===200&&JSON.stringify(replayHuman.data)===JSON.stringify(humanReply.data),'human_event_replays_same_result_after_gateway_restart');
  ensure(stateKey(await deskHistory())===stateKey(active),'human_replay_does_not_duplicate_or_change_attention');
  activeStep='human_backend_restart';
  const oldStaff=staff,oldCustomer=a,oldCustomerToken=sa.access_token;
  await stop(host,'SIGKILL');host=await startBackend(hostPort);
  ensure((await request('/desk/queue',{cookie:oldStaff})).status===401,'backend_restart_rejects_old_staff_cookie');
  for(const operation of ['claim','reply','close'])ensure((await action(oldStaff,{action:operation,event_id:'stale-staff',content:'must reject',revision:active.revision})).status===401,'old_staff_'+operation+'_denied_after_restart');
  ensure((await request('/host/chat-session',{method:'POST',cookie:oldCustomer,body:{}})).status===401,'human_backend_restart_rejects_old_customer_cookie');
  staff=await staffLogin('staff-1');otherStaff=await staffLogin('staff-2');a=await login('lin');b=await login('zhou');sa=await session(a);sb=await session(b);
  ensure(sa.conversation_id===originalConversation&&sb.conversation_id===otherConversation,'human_restart_stable_customer_mapping');
  ensure((await request('/chat/v1/messages',{cookie:a,token:oldCustomerToken})).status===401,'human_restart_rejects_previous_capability');
  ensure((await request('/chat/v1/messages',{cookie:b,token:sa.access_token})).status===401,'human_restart_cross_customer_denied');
  ensure(stateKey(await deskHistory())===stateKey(active),'claimed_state_and_attention_survive_backend_restart');
  await ownership('backend_restart');
  const backendReplay=await action(staff,JSON.parse(fs.readFileSync(output+'/pending-human-event.json')));
  ensure(backendReplay.status===200&&JSON.stringify(backendReplay.data)===JSON.stringify(humanReply.data),'human_reply_replay_after_backend_restart');
  ensure(stateKey(await deskHistory())===stateKey(active),'backend_replay_no_duplicate');
  const customerHuman=await history(a,sa);
  ensure(customerHuman.messages.filter(m=>m.content===replyEvent.content).length===1,'customer_sees_exactly_one_human_reply');
  ensure(!(await history(b,sb)).messages.some(m=>m.content===replyEvent.content),'other_customer_cannot_see_human_reply');
  activeStep='closed_state_restart';
  ensure((await action(staff,{action:'close',revision:active.revision})).status===200,'close_before_restart');
  const closedState=await deskHistory();ensure(closedState.status==='closed','closed_state_confirmed');
  await stop(gateway,'SIGKILL');gateway=await startGateway(gatewayPort);
  ensure((await request('/chat/v1/messages',{cookie:a,token:sa.access_token})).status===401,'closed_restart_rejects_old_capability_before_exchange');
  sa=await session(a);
  ensure(sa.conversation_id===originalConversation,'closed_restart_exchange_keeps_closed_conversation');
  ensure(stateKey(await deskHistory())===stateKey(closedState),'closed_state_and_history_survive_restart');
  ensure(!(await queue(staff)).some(row=>row.conversation_id===originalConversation),'closed_not_in_active_queue');
  ensure((await request('/chat/v1/messages',{method:'POST',cookie:a,token:sa.access_token,body:{event_id:'closed-customer-send',content:'must reject'}})).status===409,'closed_customer_send_denied_after_restart');
  ensure((await action(staff,{action:'reply',event_id:'closed-staff-reply',content:'must reject'})).status===409,'closed_staff_reply_denied_after_restart');
  report.passed=true;
}catch{report.failure_step=activeStep;report.passed=false;process.exitCode=1;}
finally{
  if(proxy){proxy.closeAllConnections();await new Promise(resolve=>proxy.close(resolve));}
  for(const entry of children.slice().reverse())await stop(entry);
  if(dbStarted){try{await docker('stop','postgres');report.database_stopped=true;}catch{report.database_stopped=false;report.passed=false;process.exitCode=1;}}
  await rm(directory,{recursive:true,force:true});report.finished_at=new Date().toISOString();save();
  console.log(JSON.stringify({passed:report.passed,failure_step:report.failure_step,report:output+'/report.json',database_retained:true,database_stopped:report.database_stopped}));
}
