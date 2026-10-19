// Only the explicitly authorized local CRMEB test instance. Credentials via stdin.
import crypto from 'node:crypto';
import {createCRMEBAPI} from '../../web/chat/src/crmeb.mjs';
import {savePending,readPending} from '../../web/chat/src/protocol.mjs';
import {reconcilePending} from '../../web/chat/src/pending-receipt.mjs';
let raw='';for await(const chunk of process.stdin)raw+=chunk;
const credentials=JSON.parse(raw);raw='';
const origin='http://demo.crmeb.com',tokens=[],checks=[],network=[];let staff='',agent='';
const ensure=(ok,name)=>{if(!ok)throw Error(name);checks.push(name);};
async function request(path,method='GET',token='',body,cap=''){
 const headers={Origin:origin};if(token)headers['Authori-zation']='Bearer '+token;
 if(cap)headers['X-Omni-Chat-Token']=cap;if(body!==undefined)headers['Content-Type']='application/json';
 const r=await fetch(origin+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(15000)});
 return {status:r.status,data:await r.json()};
}
const m=new Map(),storage={getItem:k=>m.get(k)??null,setItem:(k,v)=>m.set(k,v),removeItem:k=>m.delete(k)};
let login='',inject=false;
const api=createCRMEBAPI({getLogin:()=>login,fetcher:async(path,init)=>{
 const r=await fetch(origin+path,{...init,headers:{...init.headers,Origin:origin}});
 network.push({path,method:init.method,status:r.status});
 if(inject&&path==='/api/omni-chat/messages'&&init.method==='POST'){
  inject=false;ensure(r.status===200,'original_message_accepted_before_fault');
  await r.arrayBuffer();return new Response('{"error":"unavailable"}',{status:503,headers:{'Content-Type':'application/json'}});
 }
 return r;
}});
const pending={event_id:crypto.randomUUID(),content:'CRMEB只读回执验收 '+crypto.randomUUID()};
let conversation='';
try{
 for(const user of credentials.users){const r=await request('/api/login','POST','',user);ensure(!!r.data?.data?.token,'customer_login_'+tokens.length);tokens.push(r.data.data.token);}
 const sr=await request('/kefuapi/login','POST','',credentials.staff);staff=sr.data?.data?.token;ensure(!!staff,'staff_login');
 const me=await request('/api/omni-chat/desk/me','GET',staff);agent=me.data.agent?.id;ensure(me.status===200&&!!agent,'staff_bound');
 login=tokens[0];let session=await api.open();ensure(session.reply_mode==='fake'&&session.human_enabled,'fake_mode_required');
 let h=await api.history();
 if(h.status==='closed'){await api.nextRound();session=await api.open();h=await api.history();}
 ensure(h.status==='bot_active','fresh_bot_round_required');
 conversation=session.conversation_id;savePending(storage,conversation,pending);
 inject=true;try{await api.send(pending);throw Error('fault_not_observed');}catch(e){ensure(e.status===503,'frontend_sees_lost_success');}
 ensure(!!readPending(storage,conversation),'pending_retained_after_fault');
 const binding=await request('/api/omni-chat/session','POST',tokens[0],{});
 ensure((await request('/api/omni-chat/message-receipt','POST',tokens[1],pending,binding.data.access_token)).status===401,'mixed_login_capability_rejected');
 login=tokens[1];api.clear();const bs=await api.open();
 ensure(bs.conversation_id!==conversation,'different_customer_round');
 ensure((await api.receipt(pending)).accepted===false,'other_customer_cannot_find_event');
 ensure(!!readPending(storage,conversation),'switch_preserves_original_cache');
 login=tokens[0];api.clear();session=await api.open();
 ensure(session.conversation_id===conversation,'return_restores_same_round');
 ensure(await reconcilePending({api,storage,conversationID:conversation,pending,active:()=>true}),'refresh_confirms_exact_event');
 ensure(readPending(storage,conversation)===null,'confirmed_cache_removed');
 const unknown={...pending,event_id:crypto.randomUUID()};savePending(storage,conversation,unknown);
 ensure(!await reconcilePending({api,storage,conversationID:conversation,pending:unknown,active:()=>true}),'unknown_not_confirmed');
 ensure(!!readPending(storage,conversation),'unknown_cache_retained');
 try{await api.receipt({...pending,content:'wrong'});throw Error('conflict_missing');}catch(e){ensure(e.status===409,'content_conflict');}
 const handoff=await api.handoff();
 const action=body=>request('/api/omni-chat/desk/action','POST',staff,{conversation_id:conversation,expected_agent_id:agent,...body});
 const claim=await action({action:'claim',revision:handoff.revision});ensure(claim.status===200,'staff_claim');
 ensure((await action({action:'close',revision:claim.data.revision})).status===200,'staff_close');
 savePending(storage,conversation,pending);api.clear();await api.open();
 ensure((await api.history()).status==='closed','reload_keeps_closed');
 ensure(await reconcilePending({api,storage,conversationID:conversation,pending,active:()=>true}),'closed_reload_confirms');
 h=await api.history();ensure(h.status==='closed'&&h.messages.filter(x=>x.content===pending.content).length===1,'single_message_still_closed');
 ensure(network.filter(x=>x.path==='/api/omni-chat/messages'&&x.method==='POST').length===1,'no_automatic_resend');
 await request('/api/logout','GET',tokens[0]);
 ensure((await request('/api/omni-chat/message-receipt','POST',tokens[0],pending,binding.data.access_token)).status===401,'logout_revokes_receipt');
 console.log(JSON.stringify({passed:true,checks,network,conversation_id:conversation,external_model_calls:0,fault_layer:'client transport replaces successful real HTTP send with 503; shared production recovery helper, not browser UI'},null,2));
}catch(e){console.log(JSON.stringify({passed:false,checks,network,error:e.message},null,2));process.exitCode=1;}
finally{api.clear();for(const t of tokens)await request('/api/logout','GET',t).catch(()=>{});if(staff)await request('/kefuapi/user/logout','POST',staff,{}).catch(()=>{});}
