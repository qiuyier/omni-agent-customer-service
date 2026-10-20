import {createInterface} from 'node:readline';
import {randomUUID} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {writeFileSync} from 'node:fs';
const lines=createInterface({input:process.stdin});
const credentials=await new Promise(resolve=>lines.once('line',s=>{lines.close();resolve(JSON.parse(s));}));
const origin='http://demo.crmeb.com',checks=[],tokens=[];
let staff='',conversation='',message='',report,needsRecovery=false;
const recover=()=>execFileSync('docker',['--context','desktop-linux','compose','-f','deploy/crmeb/compose.yaml','up','-d','--force-recreate','postgres','gateway'],{cwd:'/Users/LOCAL_USER/Code/GitHub/omni-agent-customer-service',stdio:'ignore',timeout:60000});
const ensure=(ok,name)=>{if(!ok)throw Error(name);checks.push(name);};
async function call(path,method='GET',login='',body,cap=''){
 const r=await fetch(origin+path,{method,headers:{Origin:origin,...(login?{'Authori-zation':'Bearer '+login}:{}),...(cap?{'X-Omni-Chat-Token':cap}:{}),...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(15000)});
 return {status:r.status,data:await r.json()};
}
async function ready(){
 for(let n=0;n<30;n++){
  try{execFileSync('docker',['--context','desktop-linux','exec','php74','php','-r','$r=@file_get_contents("http://127.0.0.1:8093/readyz");exit($r===false?1:0);'],{stdio:'ignore',timeout:5000});return;}catch{}
  await new Promise(r=>setTimeout(r,500));
 }throw Error('gateway_not_ready');
}
try{
 for(const user of credentials.users){const r=await call('/api/login','POST','',user);ensure(!!r.data?.data?.token,'customer_login_'+tokens.length);tokens.push(r.data.data.token);}
 const sr=await call('/kefuapi/login','POST','',credentials.staff);staff=sr.data?.data?.token;ensure(!!staff,'staff_login');
 const me=await call('/api/omni-chat/desk/me','GET',staff);ensure(me.status===200&&!!me.data.agent?.id,'staff_identity');
 ensure((await call('/api/omni-chat/desk/me','GET',tokens[0])).status===401,'customer_cannot_be_staff');
 const open=i=>call('/api/omni-chat/session','POST',tokens[i],{});
 let sessions=[];for(let i=0;i<2;i++){const r=await open(i);ensure(r.status===200&&r.data.reply_mode==='fake'&&r.data.storage==='postgres'&&r.data.human_enabled,'fake_postgres_'+i);sessions.push(r.data);}
 const query=(i,path,body)=>call('/api/omni-chat/'+path,body===undefined?'GET':'POST',tokens[i],body,sessions[i].access_token);
 let h=await query(0,'messages');ensure(h.status===200,'history_read');
 if(h.data.status==='closed'){ensure((await query(0,'consultations',{})).status===200,'new_test_round');sessions[0]=(await open(0)).data;h=await query(0,'messages');}
 ensure(h.data.status==='bot_active','idle_test_round_required');
 conversation=sessions[0].conversation_id;
 const pending={event_id:randomUUID(),content:'通用接入试运行重启验收 '+randomUUID()};
 const sent=await query(0,'messages',pending);ensure(sent.status===200&&!!sent.data.message_id,'test_message_sent');message=sent.data.message_id;
 const accepted=r=>r.status===200&&r.data.accepted===true&&r.data.message_id===message&&r.data.event_id===pending.event_id&&r.data.conversation_id===conversation;
 ensure(accepted(await query(0,'message-receipt',pending)),'receipt_before_restart');
 const unknown=await query(0,'message-receipt',{...pending,event_id:randomUUID()});ensure(unknown.status===200&&unknown.data.accepted===false&&!unknown.data.message_id,'unknown_receipt');
 ensure((await query(0,'message-receipt',{...pending,content:'conflicting'})).status===409,'receipt_conflict');
 const other=await query(1,'message-receipt',pending);ensure(other.status===200&&other.data.accepted===false&&!other.data.message_id,'receipt_customer_isolation');
 ensure((await call('/api/omni-chat/message-receipt','POST',tokens[1],pending,sessions[0].access_token)).status===401,'mixed_login_denied');
 const handoff=await query(0,'handoff',{});ensure(handoff.status===200,'handoff');
 const action=body=>call('/api/omni-chat/desk/action','POST',staff,{conversation_id:conversation,expected_agent_id:me.data.agent.id,...body});
 const claim=await action({action:'claim',revision:handoff.data.revision});ensure(claim.status===200,'claim');
 const reply={action:'reply',event_id:randomUUID(),content:'试运行人工回复'};
 ensure((await action(reply)).status===200,'staff_reply');ensure((await action(reply)).status===200,'staff_reply_retry');
 const before=[];for(let i=0;i<2;i++){const r=await query(i,'messages');ensure(r.status===200,'before_restart_history_'+i);before.push(r.data);}
 execFileSync('docker',['--context','desktop-linux','restart','omni-crmeb-webchat-test-gateway-1'],{stdio:'ignore',timeout:60000});await ready();checks.push('gateway_restart_ready');
 for(let i=0;i<2;i++){
  ensure((await query(i,'message-receipt',pending)).status===401,'restart_old_capability_denied_'+i);
  const r=await open(i);ensure(r.status===200&&r.data.conversation_id===sessions[i].conversation_id&&r.data.access_token!==sessions[i].access_token,'same_conversation_renewal_'+i);sessions[i]=r.data;
  ensure(JSON.stringify((await query(i,'messages')).data)===JSON.stringify(before[i]),'exact_history_after_restart_'+i);
 }
 ensure(accepted(await query(0,'message-receipt',pending)),'exact_receipt_after_restart');
 needsRecovery=true;
 execFileSync('docker',['--context','desktop-linux','stop','omni-crmeb-webchat-test-gateway-1','omni-crmeb-webchat-test-postgres-1'],{stdio:'ignore',timeout:60000});
 execFileSync('docker',['--context','desktop-linux','restart','php74'],{stdio:'ignore',timeout:60000});
 recover();await ready();needsRecovery=false;checks.push('php74_and_private_services_ready');
 for(let i=0;i<2;i++){
  ensure((await query(i,'message-receipt',pending)).status===401,'php_restart_old_capability_denied_'+i);
  const r=await open(i);ensure(r.status===200&&r.data.conversation_id===sessions[i].conversation_id,'php_restart_login_and_conversation_preserved_'+i);sessions[i]=r.data;
  ensure(JSON.stringify((await query(i,'messages')).data)===JSON.stringify(before[i]),'php_restart_exact_history_'+i);
 }
 ensure(accepted(await query(0,'message-receipt',pending)),'php_restart_exact_receipt');
 const state=await action({action:'history'});ensure(state.status===200&&state.data.status==='human_active','staff_ownership_after_restart');
 ensure((await action({action:'close',revision:state.data.revision})).status===200,'close_test_round');
 ensure(accepted(await query(0,'message-receipt',pending)),'closed_receipt');
 const closed=await query(0,'messages');ensure(closed.data.status==='closed'&&JSON.stringify(closed.data.messages)===JSON.stringify(before[0].messages),'closed_receipt_read_only');
 ensure((await call('/api/logout','GET',tokens[0])).status===200,'customer_logout');
 ensure((await query(0,'message-receipt',pending)).status===401,'logout_receipt_denied');
 ensure((await call('/kefuapi/user/logout','POST',staff,{})).status===200,'staff_logout');
 ensure((await action({action:'history'})).status===401,'staff_logout_denied');
 report={passed:true};
}catch(e){report={passed:false,failed_check:/^[a-z0-9_]+$/.test(e.message)?e.message:'transport_failure'};}
finally{
 if(needsRecovery)try{recover();await ready();checks.push('failure_recovery_ready');}catch{checks.push('failure_recovery_failed');}
 let failures=0;
 for(const token of tokens)try{const r=await call('/api/logout','GET',token);if(r.status!==200)failures++;}catch{failures++;}
 if(staff)try{const r=await call('/kefuapi/user/logout','POST',staff,{});if(r.status!==200)failures++;}catch{failures++;}
 report={...report,checked_at:new Date().toISOString(),checks,conversation_id:conversation,message_id:message,external_model_calls:0,cleanup_failures:failures,scope:'real CRMEB JWT / fake / PostgreSQL / dedicated gateway and authorized PHP74 restart'};
 if(failures)report.passed=false;
 writeFileSync('/tmp/crmeb-trial-restart-report.json',JSON.stringify(report,null,2)+'\n');
 console.log(JSON.stringify(report));process.exitCode=report.passed?0:1;
}
