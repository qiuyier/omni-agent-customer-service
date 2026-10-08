// Authorized local CRMEB acceptance only. Login credentials via stdin; no token output.
import fs from 'node:fs';
let raw='';for await(const c of process.stdin)raw+=c;const users=JSON.parse(raw);raw='';
const origin='http://demo.crmeb.com',checks=[],tokens=[],sessions=[],answers=[];
const run=Date.now().toString(36), markers=['海棠-'+run,'琥珀-'+run];let generated=0;
const ensure=(ok,name)=>{if(!ok)throw Error(name);checks.push(name);};
async function call(path,method='GET',login,cap,body){
 const headers={Origin:origin};if(login)headers['Authori-zation']='Bearer '+login;if(cap)headers['X-Omni-Chat-Token']=cap;if(body!==undefined)headers['Content-Type']='application/json';
 const r=await fetch(origin+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(150000)});let data;try{data=await r.json();}catch{data={};}return{status:r.status,data};
}
const exchange=i=>call('/api/omni-chat/session','POST',tokens[i],null,{});
const history=i=>call('/api/omni-chat/messages','GET',tokens[i],sessions[i].access_token);
async function send(i,body){let r=await call('/api/omni-chat/messages','POST',tokens[i],sessions[i].access_token,body);if(r.status===401){const s=await exchange(i);ensure(s.status===200&&s.data.conversation_id===sessions[i].conversation_id,'renew_same_conversation');sessions[i]=s.data;r=await call('/api/omni-chat/messages','POST',tokens[i],sessions[i].access_token,body);}return r;}
try{
 for(const u of users){const r=await call('/api/login','POST',null,null,u);if(!r.data?.data?.token)throw Error('login');tokens.push(r.data.data.token);}
 for(let i=0;i<2;i++){const r=await exchange(i);ensure(r.status===200&&r.data.storage==='postgres'&&r.data.reply_mode==='ai','ai_session_'+i);sessions.push(r.data);}
 ensure(sessions[0].conversation_id!==sessions[1].conversation_id,'distinct_conversations');
 for(let turn=0;turn<2;turn++)for(let i=0;i<2;i++){
  const body={event_id:'ai-'+run+'-'+i+'-'+turn,content:turn===0?'这是虚构的上下文隔离测试。本轮我的测试代号是“'+markers[i]+'”，我偏好'+(i===0?'简洁的文字说明':'分步骤的文字说明')+'。请简短确认，不要查询或执行任何业务操作。':'请根据我本轮刚才提供的信息，复述我的测试代号和说明偏好。'};
  const started=Date.now(),r=await send(i,body);ensure(r.status===200&&r.data.reply_message_id,'real_ai_reply_'+i+'_'+turn);generated++;
  const h=await history(i);ensure(h.status===200,'history_'+i+'_'+turn);const answer=h.data.messages.find(m=>m.id===r.data.reply_message_id);
  ensure(answer?.role==='bot'&&answer.content&&!answer.content.includes('这是一条用于验证网页客服持久化接入流程的演示回复'),'not_fake_'+i+'_'+turn);
  ensure(!answer.content.includes(markers[1-i])&&!h.data.messages.some(m=>m.content.includes(markers[1-i])),'no_other_account_marker_'+i+'_'+turn);
  if(turn===1)ensure(answer.content.includes(markers[i]),'multi_turn_context_'+i);
  answers.push({account:i+1,turn:turn+1,question:body.content,answer:answer.content,elapsed_ms:Date.now()-started});
  const replay=await send(i,body);ensure(replay.status===200&&replay.data.message_id===r.data.message_id&&replay.data.reply_message_id===r.data.reply_message_id,'replay_same_ids_'+i+'_'+turn);
  ensure(JSON.stringify((await history(i)).data.messages)===JSON.stringify(h.data.messages),'replay_unchanged_history_'+i+'_'+turn);
  ensure((await send(i,{...body,content:'changed content'})).status===409,'conflict_'+i+'_'+turn);
 }
 const before=(await history(0)).data.messages,s=await exchange(0);ensure(s.status===200&&s.data.conversation_id===sessions[0].conversation_id,'refresh_same_conversation');sessions[0]=s.data;ensure(JSON.stringify((await history(0)).data.messages)===JSON.stringify(before),'refresh_exact_history');
 ensure((await call('/api/omni-chat/messages','GET',tokens[1],sessions[0].access_token)).status===401,'cross_account_rejected');
 console.log(JSON.stringify({passed:true,checks:checks.length,successful_real_replies:generated}));
 fs.writeFileSync('integrations/crmeb/ai-acceptance-report.json',JSON.stringify({passed:true,checked_at:new Date().toISOString(),checks,successful_real_replies:generated,answers,knowledge_enabled:false,notes:'Small synthetic-context sample, not a general answer-quality guarantee. Provider internal retries not counted.'},null,2)+'\n');
}catch(e){const name=/^[a-z0-9_]+$/.test(e.message||'')?e.message:'acceptance_failed';console.log(JSON.stringify({passed:false,checks,failed_check:name,successful_real_replies:generated}));process.exitCode=1;}
finally{for(const t of tokens)await call('/api/logout','GET',t).catch(()=>{});}
