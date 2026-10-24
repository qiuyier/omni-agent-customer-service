// Local disposable CRMEB acceptance. Credentials enter through stdin, never reports.
import crypto from 'node:crypto';
let raw='';for await(const part of process.stdin)raw+=part;
const credentials=JSON.parse(raw);raw='';
const origin='http://demo.crmeb.com',checks=[],customers=[];let staff='';
const ensure=(ok,name)=>{if(!ok)throw Error(name);checks.push(name);};
async function call(path,method='GET',token='',body,capability=''){
 const headers={Origin:origin};if(token)headers['Authori-zation']='Bearer '+token;
 if(capability)headers['X-Omni-Chat-Token']=capability;
 if(body!==undefined)headers['Content-Type']='application/json';
 const r=await fetch(origin+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(10000)});
 let data;try{data=await r.json();}catch{data={};}return {status:r.status,data};
}
try{
 for(const u of credentials.users){const r=await call('/api/login','POST','',u);ensure(!!r.data?.data?.token,'customer_login_'+customers.length);customers.push(r.data.data.token);}
 const login=await call('/kefuapi/login','POST','',credentials.staff);staff=login.data?.data?.token;ensure(!!staff,'staff_login');
 const me=await call('/api/omni-chat/desk/me','GET',staff);ensure(me.status===200&&me.data.agent?.id==='crmeb-kefu-1','mapped_staff');
 ensure((await call('/api/omni-chat/desk/me','GET',customers[0])).status===401,'customer_cannot_be_staff');
 ensure((await call('/api/omni-chat/session','POST',staff,{})).status===401,'staff_cannot_be_customer');
 const sessions=[];for(const t of customers){const r=await call('/api/omni-chat/session','POST',t,{});ensure(r.status===200&&r.data.human_enabled&&r.data.reply_mode==='fake','fake_human_session_'+sessions.length);sessions.push(r.data);}
 let [a,b]=sessions;ensure(a.conversation_id!==b.conversation_id,'customer_isolation');
 // Existing test accounts may be left on a closed consultation. Start a new
 // round through the public API; never erase history or steal an active desk.
 const initial=await call('/api/omni-chat/messages','GET',customers[0],undefined,a.access_token);
 ensure(initial.status===200,'initial_history');
 if(initial.data.status==='closed'){
  const next=await call('/api/omni-chat/consultations','POST',customers[0],{},a.access_token);
  ensure(next.status===200,'start_after_existing_closed_round');
  const fresh=await call('/api/omni-chat/session','POST',customers[0],{});
  ensure(fresh.status===200&&fresh.data.conversation_id===next.data.conversation_id,'bind_fresh_round');a=fresh.data;
 }else ensure(initial.data.status==='bot_active','initial_round_not_bot_active');
 ensure((await call('/api/omni-chat/handoff','POST',customers[1],{},a.access_token)).status===401,'foreign_handoff_rejected');
 const client=(path,method,body)=>call('/api/omni-chat/'+path,method,customers[0],body,a.access_token);
 const action=body=>call('/api/omni-chat/desk/action','POST',staff,{conversation_id:a.conversation_id,expected_agent_id:me.data.agent.id,...body});
 const content='人工链路自动验收 '+crypto.randomUUID();
 const sentCustomer=await client('messages','POST',{event_id:crypto.randomUUID(),content});
 ensure(sentCustomer.status===200,'customer_message_http_'+sentCustomer.status);
 const handoff=await client('handoff','POST',{});ensure(handoff.status===200&&handoff.data.status==='waiting_human','handoff');
 const queue=await call('/api/omni-chat/desk/queue','GET',staff);ensure(queue.data.items?.some(x=>x.conversation_id===a.conversation_id),'queue_visible');
 ensure((await action({action:'reply',event_id:crypto.randomUUID(),content:'不得在接管前发送'})).status!==200,'reply_before_claim_denied');
 const claim=await action({action:'claim',revision:handoff.data.revision});ensure(claim.status===200&&claim.data.status==='human_active','claim');
 let history=await action({action:'history'});ensure(history.status===200&&history.data.messages.some(x=>x.content===content),'staff_history');
 const ids=history.data.messages.filter(x=>x.role==='customer').map(x=>x.id);
 ensure((await action({action:'read',message_ids:ids})).status===200,'mark_read');
 const event=crypto.randomUUID(),reply='人工验收回复 '+event;
 const sent=await action({action:'reply',event_id:event,content:reply});ensure(sent.status===200,'staff_reply');
 ensure((await action({action:'reply',event_id:event,content:reply})).status===200,'reply_retry_idempotent');
 const ch=await client('messages','GET');ensure(ch.data.messages.filter(x=>x.content===reply).length===1,'customer_receives_once');
 const other=await call('/api/omni-chat/messages','GET',customers[1],undefined,b.access_token);ensure(other.status===200&&!other.data.messages.some(x=>x.content===reply),'other_customer_cannot_read');
 history=await action({action:'history'});
 ensure((await action({action:'close',revision:history.data.revision})).status===200,'close');
 ensure((await client('messages','POST',{event_id:crypto.randomUUID(),content:'关闭后不得发送'})).status!==200,'closed_send_denied');
 const next=await client('consultations','POST',{});ensure(next.status===200&&next.data.conversation_id!==a.conversation_id,'new_consultation');
 ensure((await client('messages','GET')).status===401,'old_binding_invalidated');
 const reopened=await call('/api/omni-chat/session','POST',customers[0],{});ensure(reopened.status===200&&reopened.data.conversation_id===next.data.conversation_id,'reopen_new_round');
 const empty=await call('/api/omni-chat/messages','GET',customers[0],undefined,reopened.data.access_token);ensure(empty.status===200&&empty.data.messages.length===0,'no_old_messages_in_new_round');
 const old=await action({action:'history'});ensure(old.status===200&&old.data.status==='closed'&&old.data.messages.some(x=>x.content===reply),'closed_history_preserved');
 await call('/kefuapi/user/logout','POST',staff,{});
 ensure((await call('/api/omni-chat/desk/me','GET',staff)).status===401,'staff_logout_rejected');
 console.log(JSON.stringify({passed:true,checks,external_model_calls:0}));
}catch(e){console.log(JSON.stringify({passed:false,checks,error:e.message}));process.exitCode=1;}
finally{for(const token of customers)await call('/api/logout','GET',token).catch(()=>{});if(staff)await call('/kefuapi/user/logout','POST',staff,{}).catch(()=>{});}
