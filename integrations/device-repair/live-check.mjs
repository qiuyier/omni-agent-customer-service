// Explicit opt-in, synthetic inputs only; capped provider requests, no retries.
import fs from 'node:fs';
import {parseEnv} from 'node:util';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {startDemo} from './demo.mjs';

if (process.argv.length !== 3 || process.argv[2] !== '--allow-model-api') throw Error('Requires --allow-model-api (maximum 7 provider requests)');
const root=fileURLToPath(new URL('../../',import.meta.url));
const settings=parseEnv(fs.readFileSync(root+'.env','utf8'));
if(settings.AI_PROTOCOL!=='chat_completions'||!settings.AI_API_KEY||!settings.AI_MODEL||settings.AI_SYSTEM_PROMPT) throw Error('Requires explicit chat_completions configuration without prompt override');
const endpoint=new URL(settings.AI_BASE_URL.replace(/\/$/,'')+'/chat/completions');
if(endpoint.protocol!=='https:'||endpoint.username||endpoint.password||endpoint.search||endpoint.hash)throw Error('Expected HTTPS provider endpoint without embedded credentials');
const corpus='examples/regression/knowledge/device-repair/live-cases.json';
const cases=JSON.parse(fs.readFileSync(root+corpus));
const budget=7;
if(cases.length!==6||cases.reduce((n,c)=>n+c.questions.length,0)!==budget)throw Error('Frozen case count does not match budget');
const output=root+'examples/archive/knowledge/device-repair/eval_runs/'+new Date().toISOString().replace(/[:.]/g,'-');
fs.mkdirSync(output,{recursive:true});
const hash=f=>createHash('sha256').update(fs.readFileSync(root+f)).digest('hex');
const frozenFiles=[corpus,'examples/knowledge/device-repair/faq.json','internal/bootstrap/ai.go','internal/ai/eino/reply_prompt.go','integrations/device-repair/demo.mjs','integrations/device-repair/live-check.mjs'];
const report={started_at:new Date().toISOString(),provider:settings.AI_PROVIDER,model:settings.AI_MODEL,protocol:settings.AI_PROTOCOL,transport:'custom chat_completions via bounded local relay; provider defaults otherwise',budget,provider_requests:0,retries:false,complete:false,quality_review:'pending manual review',sha256:Object.fromEntries(frozenFiles.map(f=>[f,hash(f)])),cases,results:[]};
const save=()=>fs.writeFileSync(output+'/report.json',JSON.stringify(report,null,2)+'\n',{mode:0o600});
save();
let active;
const abort=new AbortController();
const totalTimer=setTimeout(()=>abort.abort(),12*60*1000);
const stop=()=>abort.abort();
process.once('SIGINT',stop);process.once('SIGTERM',stop);
try {
  for(const c of cases){
    if(abort.signal.aborted)throw Error('run_cancelled');
    const result={id:c.id,turns:[],provider_calls:[]};report.results.push(result);save();
    active=await startDemo({modelName:settings.AI_MODEL,forward:async payload=>{
      if(abort.signal.aborted||report.provider_requests>=budget)throw Error('budget_or_cancelled');
      const messages=payload.messages.map(m=>({...m,content:Array.isArray(m.content)?m.content.map(p=>p.text||'').join(''):m.content}));
      const envelope=messages.find(m=>m.role==='user'&&m.content?.startsWith('BEGIN_UNTRUSTED_KNOWLEDGE_JSON\n'));
      const references=envelope?JSON.parse(envelope.content.split('\n')[1]).snippets:[];
      const call={index:++report.provider_requests,references,history:messages.filter(m=>m.role!=='system'&&m!==envelope)};
      result.provider_calls.push(call);save();
      let response;
      try {response=await fetch(endpoint,{method:'POST',headers:{Authorization:'Bearer '+settings.AI_API_KEY,'Content-Type':'application/json'},body:JSON.stringify({...payload,model:settings.AI_MODEL}),redirect:'error',signal:AbortSignal.any([abort.signal,AbortSignal.timeout(80000)])});}
      catch {call.failure='provider_transport_or_timeout';save();throw Error(call.failure);}
      call.http_status=response.status;
      if(!response.ok){call.failure='provider_http_error';await response.body?.cancel();save();throw Error(call.failure);}
      const body=await response.json();
      call.finish_reason=body.choices?.[0]?.finish_reason;call.usage=body.usage;save();
      return body;
    }});
    const call=async(path,body,cookie,token)=>{
      const response=await fetch(active.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:active.origin,'Content-Type':'application/json',...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:body===undefined?undefined:JSON.stringify(body),redirect:'error',signal:AbortSignal.any([abort.signal,AbortSignal.timeout(110000)])});
      if(response.status!==200)throw Error('host_http_'+response.status);
      return {data:await response.json(),cookie:response.headers.get('set-cookie')?.split(';')[0]};
    };
    const cookie=(await call('/demo/login',{account:'lin'})).cookie;
    const session=(await call('/host/chat-session',{},cookie)).data;
    for(const [i,question] of c.questions.entries()){
      const sent=(await call('/chat/v1/messages',{event_id:c.id+'-'+i,content:question},cookie,session.access_token)).data;
      if(sent.outcome!=='replied')throw Error('reply_not_completed');
      const history=(await call('/chat/v1/messages',undefined,cookie,session.access_token)).data;
      const answer=history.messages.find(m=>m.id===sent.reply_message_id)?.content;
      if(!answer)throw Error('saved_answer_missing');
      result.turns.push({question,answer});save();
      console.log(c.id+': saved turn '+(i+1));
    }
    await active.stop();active=null;
  }
  report.source_hashes_unchanged=frozenFiles.every(f=>report.sha256[f]===hash(f));
  report.complete=report.results.every((r,i)=>r.turns.length===cases[i].questions.length)&&report.source_hashes_unchanged;
} catch(error){report.failure=/^(run_cancelled|host_http_\d+|reply_not_completed|saved_answer_missing)$/.test(error.message)?error.message:'run_failed_details_redacted';process.exitCode=1;}
finally {clearTimeout(totalTimer);await active?.stop();report.finished_at=new Date().toISOString();save();console.log(JSON.stringify({report:output+'/report.json',complete:report.complete,provider_requests:report.provider_requests}));}
