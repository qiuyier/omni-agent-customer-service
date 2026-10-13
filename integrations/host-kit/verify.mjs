// Destructive-to-test-state acceptance: fake mode, fresh accounts, loopback only.
import {randomUUID} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {resolve} from 'node:path';
export async function verifyHost(config,{allowTestWrites=false}={}){
 const checks=[],clients=[];let paths,origin,report;
 const ensure=(ok,label)=>{if(!ok)throw Object.assign(Error('check_failed'),{check:label});checks.push(label);};
 const post=(body)=>({method:'POST',body});
 function makeClient(){
  let cookie='';const call=async(path,{method='GET',body,token}={})=>{
   const r=await fetch(origin+path,{method,headers:{Origin:origin,...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{}),...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(15000)});
   const set=r.headers.getSetCookie();if(set.length)cookie=set.map(x=>x.split(';')[0]).join('; ');
   const chunks=[];let size=0;if(r.body)for await(const chunk of r.body){size+=chunk.length;if(size>1048576)throw Error('response_limit');chunks.push(Buffer.from(chunk));}
   const text=Buffer.concat(chunks).toString('utf8');
   return {status:r.status,data:text?JSON.parse(text):null};
  };return call;
 }
 try{
  if(!allowTestWrites)throw Error('test_write_consent_required');
  const u=new URL(config.origin);
  if(u.protocol!=='http:'||u.hostname!=='127.0.0.1'||!u.port||u.pathname!=='/'||u.username||u.password||u.search||u.hash)throw Error('loopback_origin_required');
  origin=u.origin;
  if(!Array.isArray(config.customers)||config.customers.length!==2||!config.staff)throw Error('two_customers_and_staff_required');
  paths={me:'/host/me',session:'/host/chat-session',messages:'/chat/v1/messages',handoff:'/chat/v1/handoff',next:'/chat/v1/consultations',deskMe:'/desk/me',queue:'/desk/queue',action:'/desk/action',customerLogin:'/demo/login',customerLogout:'/demo/logout',staffLogin:'/desk/login',staffLogout:'/desk/logout',...config.paths};
  if(Object.values(paths).some(p=>typeof p!=='string'||!/^\/[A-Za-z0-9_/-]+$/.test(p)))throw Error('invalid_paths');
  const probe=makeClient();const meta=await probe(paths.me);
  ensure(meta.status===200&&meta.data.reply_mode==='fake'&&meta.data.human_enabled===true,'fake_human_preflight');
  for(let i=0;i<3;i++){const c=makeClient();clients.push(c);const r=await c(i<2?paths.customerLogin:paths.staffLogin,post(i<2?config.customers[i]:config.staff));ensure(r.status===200,'login_'+i);}
  const [a,b,staff]=clients;
  const sessions=[];for(let i=0;i<2;i++){const s=await clients[i](paths.session,post({}));ensure(s.status===200&&s.data.reply_mode==='fake','session_'+i);sessions.push(s.data);}
  ensure(sessions[0].conversation_id!==sessions[1].conversation_id,'distinct_customers');
  for(let i=0;i<2;i++){const h=await clients[i](paths.messages,{token:sessions[i].access_token});ensure(h.status===200&&h.data.status==='bot_active'&&h.data.messages?.length===0,'fresh_test_account_'+i);}
  const token=sessions[0].access_token,id=sessions[0].conversation_id;
  const customer=(path,body)=>a(path,{...(body===undefined?{}:post(body)),token});
  ensure((await b(paths.messages,{token})).status===401,'mixed_capability_denied');
  const me=await staff(paths.deskMe);ensure(me.status===200&&typeof me.data.agent?.id==='string','staff_identity');
  const action=body=>staff(paths.action,post({conversation_id:id,expected_agent_id:me.data.agent.id,...body}));
  const event=randomUUID(),content='接入包测试 '+event;
  ensure((await customer(paths.messages,{event_id:event,content})).status===200,'customer_send');
  ensure((await customer(paths.messages,{event_id:event,content})).status===200,'customer_retry');
  const handoff=await customer(paths.handoff,{});ensure(handoff.status===200&&handoff.data.status==='waiting_human','handoff');
  const q=await staff(paths.queue);ensure(q.status===200&&q.data.items.some(x=>x.conversation_id===id),'queue');
  ensure((await action({action:'claim',revision:handoff.data.revision})).status===200,'claim');
  const replyEvent=randomUUID(),reply='人工接入包测试 '+replyEvent;
  ensure((await action({action:'reply',event_id:replyEvent,content:reply})).status===200,'reply');
  ensure((await action({action:'reply',event_id:replyEvent,content:reply})).status===200,'reply_retry');
  const h=await customer(paths.messages);
  ensure(h.status===200&&h.data.messages.filter(m=>m.content===content).length===1&&h.data.messages.filter(m=>m.content===reply).length===1,'exactly_one_saved_copy');
  const other=await b(paths.messages,{token:sessions[1].access_token});ensure(other.status===200&&other.data.messages.length===0,'other_customer_isolated');
  ensure((await action({action:'read',message_ids:h.data.messages.filter(m=>m.role==='customer').map(m=>m.id)})).status===200,'read');
  const state=await action({action:'history'});ensure(state.status===200,'staff_history');
  ensure((await action({action:'close',revision:state.data.revision})).status===200,'close');
  ensure((await customer(paths.messages,{event_id:randomUUID(),content:'must reject'})).status===409,'closed_send_denied');
  const next=await customer(paths.next,{});ensure(next.status===200&&next.data.conversation_id!==id,'next_round');
  ensure((await customer(paths.messages)).status===401,'old_binding_rejected');
  const renewed=await a(paths.session,post({}));ensure(renewed.status===200&&renewed.data.conversation_id===next.data.conversation_id,'new_session');
  const empty=await a(paths.messages,{token:renewed.data.access_token});ensure(empty.status===200&&empty.data.messages.length===0,'new_round_empty');
  await a(paths.customerLogout,post({}));ensure((await a(paths.messages,{token:renewed.data.access_token})).status===401,'customer_logout');
  await staff(paths.staffLogout,post({}));ensure((await staff(paths.queue)).status===401,'staff_logout');
  return report={passed:true,checks};
 }catch(e){return report={passed:false,checks,error:['test_write_consent_required','loopback_origin_required','two_customers_and_staff_required','invalid_paths'].includes(e.message)?e.message:'acceptance_failed',failed_check:e.check||'transport_or_configuration',failed_after:checks.at(-1)||null};}
 finally{
  let failures=0;
  for(let i=0;i<clients.length;i++)try{const r=await clients[i](i<2?paths.customerLogout:paths.staffLogout,post({}));if(![200,204].includes(r.status))failures++;}catch{failures++;}
  if(report&&failures){report.passed=false;report.cleanup_failures=failures;}
 }
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 let raw='';try{for await(const part of process.stdin){raw+=part;if(Buffer.byteLength(raw)>65536)throw Error();}const result=await verifyHost(JSON.parse(raw),{allowTestWrites:process.argv.length===3&&process.argv[2]==='--allow-test-writes'});console.log(JSON.stringify(result));process.exitCode=result.passed?0:1;}catch{console.log(JSON.stringify({passed:false,error:'invalid_input'}));process.exitCode=1;}
}
