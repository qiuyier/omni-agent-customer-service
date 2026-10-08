// Local persistent deployment acceptance. Credentials arrive only on stdin.
import {execFileSync} from 'node:child_process';
import fs from 'node:fs';
let raw='';for await(const c of process.stdin)raw+=c;const users=JSON.parse(raw);raw='';
const origin='http://demo.crmeb.com', checks=[],tokens=[],sessions=[];
const run=Date.now().toString(36), compose=['compose','-f','deploy/crmeb/compose.yaml'];
const ensure=(ok,name)=>{if(!ok)throw Error(name);checks.push(name);};
const docker=(...args)=>execFileSync('docker',[...compose,...args],{stdio:'ignore',timeout:60000});
async function call(path,method='GET',login,cap,body){
 const headers={Origin:origin};if(login)headers['Authori-zation']='Bearer '+login;if(cap)headers['X-Omni-Chat-Token']=cap;if(body!==undefined)headers['Content-Type']='application/json';
 const r=await fetch(origin+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(12000)});let data;try{data=await r.json();}catch{data={};}return{status:r.status,data};
}
const session=i=>call('/api/omni-chat/session','POST',tokens[i],null,{});
const history=i=>call('/api/omni-chat/messages','GET',tokens[i],sessions[i].access_token);
const send=(i,body)=>call('/api/omni-chat/messages','POST',tokens[i],sessions[i].access_token,body);
async function ready(){for(let n=0;n<30;n++){try{execFileSync('docker',['exec','php74','php','-r','$r=@file_get_contents("http://127.0.0.1:8093/readyz");exit($r===false?1:0);'],{stdio:'ignore',timeout:5000});return;}catch{}await new Promise(r=>setTimeout(r,500));}throw Error('readiness');}
const bodies=[0,1].map(i=>({event_id:'persist-'+run+'-'+i,content:'Persistence acceptance '+run+' user '+i}));
let needsRecovery=false;
try{
 for(const u of users){const r=await call('/api/login','POST',null,null,u);if(!r.data?.data?.token)throw Error('login');tokens.push(r.data.data.token);}
 for(let i=0;i<2;i++){const r=await session(i);ensure(r.status===200&&r.data.storage==='postgres'&&r.data.reply_mode==='fake','persistent_session_'+i);sessions.push(r.data);ensure((await send(i,bodies[i])).status===200,'send_'+i);}
 ensure(sessions[0].conversation_id!==sessions[1].conversation_id,'different_conversations');
 const before=[];for(let i=0;i<2;i++){const h=await history(i);ensure(h.status===200&&h.data.messages.some(m=>m.content===bodies[i].content)&&!h.data.messages.some(m=>m.content===bodies[1-i].content),'isolated_history_'+i);before.push(h.data.messages);}
 ensure((await call('/api/omni-chat/messages','GET',tokens[1],sessions[0].access_token)).status===401,'cross_account_capability_rejected');
 docker('restart','gateway');await ready();
 for(let i=0;i<2;i++){
  ensure((await history(i)).status===401,'restart_old_capability_rejected_'+i);
  const r=await session(i);ensure(r.status===200&&r.data.conversation_id===sessions[i].conversation_id&&r.data.access_token!==sessions[i].access_token,'restart_renewal_same_conversation_'+i);sessions[i]=r.data;
  ensure(JSON.stringify((await history(i)).data.messages)===JSON.stringify(before[i]),'gateway_restart_exact_history_'+i);
 }
 ensure((await send(0,bodies[0])).status===200,'replay_accepted');
 ensure(JSON.stringify((await history(0)).data.messages)===JSON.stringify(before[0]),'replay_no_duplicate');
 ensure((await send(0,{...bodies[0],content:'changed body'})).status===409,'changed_replay_rejected');
 docker('restart','postgres');await ready();
 ensure(JSON.stringify((await history(0)).data.messages)===JSON.stringify(before[0]),'database_restart_exact_history');
 needsRecovery=true;docker('stop','gateway');
 const pending={event_id:'outage-'+run,content:'Recovery acceptance '+run};
 const outage=await send(0,pending);ensure([502,503].includes(outage.status)&&!JSON.stringify(outage.data).includes('postgres://'),'outage_fails_closed');
 docker('start','gateway');await ready();needsRecovery=false;
 ensure((await history(0)).status===401,'outage_old_capability_rejected');
 const renewed=await session(0);ensure(renewed.status===200&&renewed.data.conversation_id===sessions[0].conversation_id,'outage_session_recovered');sessions[0]=renewed.data;
 ensure((await send(0,pending)).status===200,'outage_same_event_retry');
 ensure((await send(0,pending)).status===200,'outage_replay');
 const after=await history(0);ensure(after.data.messages.filter(m=>m.content===pending.content&&m.role==='customer').length===1,'outage_retry_no_duplicate');
 await call('/api/logout','GET',tokens[0]);ensure((await history(0)).status===401,'logout_revokes_history');ensure((await session(0)).status===401,'logout_revokes_exchange');
 // The second account must recover its own old token independently.
 await history(1);const b=await session(1);ensure(b.status===200&&b.data.conversation_id===sessions[1].conversation_id,'other_account_survives_logout');sessions[1]=b.data;
 ensure(JSON.stringify((await history(1)).data.messages)===JSON.stringify(before[1]),'other_account_history_unchanged');
 const result={passed:true,checked_at:new Date().toISOString(),checks,external_model_calls:0,storage:'postgres',scope:'gateway/database restart, outage, replay, account isolation; wall-clock token expiry not exercised'};
 fs.writeFileSync('integrations/crmeb/persistent-acceptance-report.json',JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify(result));
}catch(e){console.log(JSON.stringify({passed:false,checks,failed_check:e.message&&/^[a-z0-9_]+$/.test(e.message)?e.message:'acceptance_failed'}));process.exitCode=1;}
finally{if(needsRecovery){try{docker('start','gateway');}catch{process.exitCode=1;}}for(const t of tokens)await call('/api/logout','GET',t).catch(()=>{});}
