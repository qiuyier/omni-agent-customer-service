// Real model calls with synthetic fixtures only. Credentials via stdin.
import fs from 'node:fs';
import {parseEnv} from 'node:util';
import {spawn,execFileSync} from 'node:child_process';
let input='';for await(const c of process.stdin)input+=c;const user=JSON.parse(input);input='';
const run=Date.now().toString(36),origin='http://demo.crmeb.com',checks=[],answers=[],versions=[];
const reportFile=process.argv[2]==='--history-boundary-v2'?'integrations/crmeb/knowledge-history-boundary-v2-report.json':process.argv[2]==='--history-boundary'?'integrations/crmeb/knowledge-history-boundary-report.json':'integrations/crmeb/knowledge-acceptance-report.json';
let login='',session,child;const ensure=(ok,label)=>{if(!ok)throw Error(label);checks.push(label);};
async function call(base,path,method,body,headers={}){const r=await fetch(base+path,{method,headers:{Origin:base,'Content-Type':'application/json',...headers},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(150000)});let data;try{data=await r.json();}catch{data={};}return{status:r.status,data};}
const headers=()=>({'Authori-zation':'Bearer '+login,...(session?{'X-Omni-Chat-Token':session.access_token}:{})});
async function exchange(){const r=await call(origin,'/api/omni-chat/session','POST',{},headers());ensure(r.status===200&&r.data.reply_mode==='ai','session');if(session)ensure(r.data.conversation_id===session.conversation_id,'same_conversation');session=r.data;}
const history=()=>call(origin,'/api/omni-chat/messages','GET',undefined,headers());
async function ask(label,question){let body={event_id:'knowledge-'+run+'-'+label,content:question};let r=await call(origin,'/api/omni-chat/messages','POST',body,headers());if(r.status===401){await exchange();r=await call(origin,'/api/omni-chat/messages','POST',body,headers());}ensure(r.status===200&&r.data.reply_message_id,label+'_reply');const h=await history();ensure(h.status===200,label+'_history');const reply=h.data.messages.find(m=>m.id===r.data.reply_message_id);ensure(reply?.content,label+'_saved');answers.push({label,question,answer:reply.content});return h.data.messages;}
function reload(fixture){const r=JSON.parse(execFileSync(process.execPath,['deploy/crmeb/knowledge.mjs',fixture],{encoding:'utf8',stdio:['ignore','pipe','pipe'],timeout:20000}));ensure(r.reloaded,fixture+'_loaded');versions.push(r);}
let outcome={passed:false};
try{
 const r=await call(origin,'/api/login','POST',user);ensure(!!r.data?.data?.token,'login');login=r.data.data.token;await exchange();reload('ecommerce-v1');
 await ask('supported','这是虚构资料验收，不询问真实商城。青禾测试商城的自提服务台什么时候开放，如何预约自提？');
 await ask('unknown','虚构青禾测试商城的退款多久到账？请只回答当前资料明确支持的内容。');
 await ask('followup','那我应该点哪个按钮催退款？这是接着上一条退款问题问的，请不要猜测功能。');
 const beforeUpdate=(await history()).data.messages;reload('ecommerce-v2');
 ensure(JSON.stringify((await history()).data.messages)===JSON.stringify(beforeUpdate),'reload_preserves_history');
 await ask('updated','请按当前有效资料重新回答：虚构青禾测试商城的自提服务台现在什么时候开放？');
 const beforeWithdraw=(await history()).data.messages;reload('withdraw');ensure(JSON.stringify((await history()).data.messages)===JSON.stringify(beforeWithdraw),'withdraw_preserves_history');
 await ask('withdrawn','请按当前有效参考资料回答虚构青禾测试商城的自提服务台现在什么时候开放；若参考资料不足，请明确说明，不以旧回复作为依据。');
 reload('ecommerce-v1');
 // Separate process, memory repository and fictional identities: never mix repair FAQs into CRMEB.
 const settings=parseEnv(fs.readFileSync('.env','utf8')),env={PATH:process.env.PATH};
 for(const k of ['AI_PROVIDER','AI_PROTOCOL','AI_MODEL','AI_BASE_URL','AI_API_KEY','AI_MAX_CONCURRENT_REPLIES','AI_SYSTEM_PROMPT'])if(settings[k])env[k]=settings[k];
 env.KNOWLEDGE_FAQ_PATH=process.cwd()+'/integrations/crmeb/knowledge-fixtures/repair-v1.json';env.KNOWLEDGE_RETRIEVER='keyword';
 child=spawn('/tmp/omni-knowledge-live-demo',['-reply','ai','-address','127.0.0.1:0'],{env,stdio:['ignore','pipe','pipe']});child.stderr.on('data',()=>{});
 const base=await new Promise((resolve,reject)=>{let out='';const timer=setTimeout(()=>reject(Error('repair_start_timeout')),15000);child.on('error',()=>{clearTimeout(timer);reject(Error('repair_start'));});child.on('exit',()=>{clearTimeout(timer);reject(Error('repair_exit'));});child.stdout.on('data',b=>{out+=b;const m=out.match(/Web chat demo: (http:\/\/127\.0\.0\.1:\d+)/);if(m){clearTimeout(timer);resolve(m[1]);}});});
 const lr=await fetch(base+'/demo/login',{method:'POST',headers:{Origin:base,'Content-Type':'application/json'},body:JSON.stringify({account:'lin'}),redirect:'error'});ensure(lr.status===200,'repair_login');const cookie=lr.headers.getSetCookie().map(x=>x.split(';')[0]).join('; ');
 const sr=await call(base,'/host/chat-session','POST',{}, {Cookie:cookie});ensure(sr.status===200,'repair_session');const auth={Cookie:cookie,Authorization:'Bearer '+sr.data.access_token};
 for(const [label,question] of [['repair_supported','虚构栖木维修服务的收件时段是什么？送修前需要做什么？'],['repair_unknown','虚构栖木维修是否支持上门，具体价格和预约入口是什么？']]){
  const sent=await call(base,'/chat/v1/messages','POST',{event_id:run+'-'+label,content:question},auth);ensure(sent.status===200&&sent.data.reply_message_id,label+'_reply');const h=await call(base,'/chat/v1/messages','GET',undefined,auth);const reply=h.data.messages.find(m=>m.id===sent.data.reply_message_id);ensure(!!reply?.content,label+'_saved');answers.push({label,question,answer:reply.content});
 }
 outcome={passed:true};
}catch(e){outcome={passed:false,failed_check:/^[a-z0-9_]+$/.test(e.message||'')?e.message:'acceptance_failed'};process.exitCode=1;}
finally{
 try{reload('ecommerce-v1');}catch{outcome.restore_failed=true;process.exitCode=1;}
 if(login)await call(origin,'/api/logout','GET',undefined,{'Authori-zation':'Bearer '+login}).catch(()=>{});
 if(child&&child.exitCode===null)await new Promise(resolve=>{const t=setTimeout(()=>child.kill('SIGKILL'),7000);child.once('exit',()=>{clearTimeout(t);resolve();});child.kill('SIGTERM');});
 const report={...outcome,checked_at:new Date().toISOString(),checks,versions,answers,quality_review:'pending_manual_review',notes:'Technical checks only; answers require review. Main fixture restored to ecommerce-v1; isolated repair process stopped.'};fs.writeFileSync(reportFile,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({...outcome,checks:checks.length,real_replies:answers.length}));
}
