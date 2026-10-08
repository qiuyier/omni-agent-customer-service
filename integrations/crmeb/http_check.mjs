// Local test environment only; credentials via stdin, never print tokens.
let raw = ''; for await (const chunk of process.stdin) raw += chunk;
const users = JSON.parse(raw); raw = '';
const origin = 'http://demo.crmeb.com', tokens = [], checks = [];
const ensure = (ok, name) => { if (!ok) throw new Error(name); checks.push(name); };
async function call(path, method = 'GET', login, capability, body, extra = {}) {
 const headers = {Origin:origin,...extra};
 if(login)headers['Authori-zation']='Bearer '+login;
 if(capability)headers['X-Omni-Chat-Token']=capability;
 if(body!==undefined)headers['Content-Type']='application/json';
 const r=await fetch(origin+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(10000)});
 let data;try{data=await r.json();}catch{data={};}return {status:r.status,data};
}
try {
 for(const u of users){const r=await call('/api/login','POST',null,null,u);if(!r.data?.data?.token)throw Error('login');tokens.push(r.data.data.token);}
 const a=await call('/api/omni-chat/session','POST',tokens[0],null,{});
 const b=await call('/api/omni-chat/session','POST',tokens[1],null,{});
 ensure(a.status===200&&b.status===200,'nginx_original_uri_clean_exchange');
 ensure(a.data.conversation_id!==b.data.conversation_id,'distinct_users');
 for(let i=0;i<2;i++){
  const session=[a,b][i].data;
  const sent=await call('/api/omni-chat/messages','POST',tokens[i],session.access_token,{event_id:'http-check-'+i,content:'HTTP adapter check '+i});
  ensure(sent.status===200,'send_'+i);
  const history=await call('/api/omni-chat/messages','GET',tokens[i],session.access_token);
  ensure(history.status===200&&history.data.messages.some(m=>m.content==='HTTP adapter check '+i)&&!history.data.messages.some(m=>m.content==='HTTP adapter check '+(1-i)),'isolated_history_'+i);
 }
 ensure((await call('/api/omni-chat/messages','GET',tokens[1],a.data.access_token)).status===401,'mixed_credentials_rejected');
 ensure((await call('/api/omni-chat/session?uid=4','POST',tokens[0],null,{})).status===400,'browser_query_rejected');
 ensure((await call('/api/omni-chat/session','POST',tokens[0],null,{customer_id:'4'})).status===400,'forged_body_rejected');
 ensure((await call('/api/omni-chat/session','POST',tokens[0],null,{}, {Origin:'http://other.test'})).status===403,'foreign_origin_rejected');
 await call('/api/logout','GET',tokens[0]);
 ensure((await call('/api/omni-chat/messages','GET',tokens[0],a.data.access_token)).status===401,'native_logout_invalidates_read');
 ensure((await call('/api/omni-chat/messages','POST',tokens[0],a.data.access_token,{event_id:'logged-out',content:'should not send'})).status===401,'native_logout_invalidates_send');
 ensure((await call('/api/omni-chat/session','POST',tokens[0],null,{})).status===401,'native_logout_invalidates_exchange');
 console.log(JSON.stringify({passed:true,checks,external_model_calls:0}));
} catch(e){console.log(JSON.stringify({passed:false,checks,error:'acceptance_failed'}));process.exitCode=1;}
finally{for(const token of tokens)await call('/api/logout','GET',token).catch(()=>{});}
