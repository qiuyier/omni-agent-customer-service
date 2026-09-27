const fs=require('fs'),assert=require('assert/strict'),crypto=require('crypto');
const root='/Users/LOCAL_USER/Code/GitHub/omni-agent-customer-service',base=root+'/examples/knowledge/webchat/eval_runs/2026-09-27/deepseek-verdict-only-v1',dest=root+'/examples/knowledge/webchat/eval_runs/2026-09-27/glm-verdict-only-v1';
const frozenBytes=fs.readFileSync(base+'/freeze.json'),prior=JSON.parse(frozenBytes),{prompt,cases,jobs}=prior;
const hash=x=>crypto.createHash('sha256').update(x).digest('hex');
assert.equal(hash(prompt),prior.prompt_sha256);assert.equal(cases.length,12);assert.equal(jobs.length,24);
const parse=raw=>({'有':'unsupported','无':'clear','无法判断':'uncertain'})[typeof raw==='string'?raw.trim():'']||'error';
for(const [raw,want]of [['有','unsupported'],['无','clear'],[' 无\n','clear'],['无法判断','uncertain'],['有。','error'],['{"result":"有"}','error'],['无，因为资料支持','error'],['','error']])assert.equal(parse(raw),want);
const model='glm-5.3-flash',endpoint='https://open.bigmodel.cn/api/paas/v4/chat/completions';
const body=c=>({model,stream:false,thinking:{type:'disabled'},max_tokens:256,messages:[{role:'system',content:prompt},{role:'user',content:JSON.stringify({reference:c.reference,history:c.history,answer:c.answer})}]});
if(!process.argv.includes('--allow-model-api')){console.log(JSON.stringify({offline_tests:'passed',prompt_hash_matches:true,planned:24,model,thinking:'disabled',max_tokens:256}));process.exit(0)}
const keys=[];for(const line of fs.readFileSync(root+'/.env.local','utf8').split(/\r?\n/)){const m=line.match(/^\s*(?:export\s+)?ZHIPUAI_API_KEY\s*=\s*(.*?)\s*$/);if(!m)continue;let v=m[1];if((v.startsWith('"')&&v.endsWith('"'))||(v.startsWith("'")&&v.endsWith("'")))v=v.slice(1,-1);else v=v.replace(/\s+#.*$/,'');keys.push(v)}
if(keys.length!==1||!keys[0]||keys[0].includes('${')||keys[0].includes('`')){console.log(JSON.stringify({attempted:0,error:'missing_or_ambiguous_glm_key'}));process.exit(1)}
if(fs.existsSync(dest))throw Error('Refusing duplicate run');fs.mkdirSync(dest,{recursive:true});
fs.writeFileSync(dest+'/freeze.json',JSON.stringify({started_at:new Date().toISOString(),endpoint,prompt,prompt_sha256:hash(prompt),runner_sha256:hash(fs.readFileSync(__filename)),baseline_freeze_sha256:hash(frozenBytes),max_requests:24,no_retries:true,model,thinking:'disabled',reasoning_effort:'omitted',max_tokens:256,cases,jobs},null,2)+'\n');fs.copyFileSync(__filename,dest+'/runner.cjs');
const results=[];
(async()=>{for(const j of jobs){const c=cases[j.case_index],r={...j,expected:c.expected};let halt=false;try{
 const resp=await fetch(endpoint,{method:'POST',redirect:'error',signal:AbortSignal.timeout(75000),headers:{Authorization:'Bearer '+keys[0],'Content-Type':'application/json'},body:JSON.stringify(body(c))});r.http_status=resp.status;
 const txt=await resp.text();if(Buffer.byteLength(txt)>1048576)throw Error('Response too large');let d;try{d=JSON.parse(txt)}catch{}
 if(!resp.ok){r.error='http_rejection';r.verdict='error';halt=true}else if(!d){r.error='invalid_envelope';r.verdict='error';halt=true}else{
 const choice=d.choices?.[0];r.returned_model=typeof d.model==='string'&&/^[a-zA-Z0-9._:-]{1,80}$/.test(d.model)?d.model:'unreported';r.finish_reason=['stop','length','tool_calls'].includes(choice?.finish_reason)?choice.finish_reason:'other';
 if(d.choices?.length!==1||choice.finish_reason!=='stop'||typeof choice.message?.content!=='string'||choice.message?.tool_calls?.length){r.error='incomplete_or_invalid_response';r.verdict='error'}else{r.raw=choice.message.content;if(r.raw.length>32768)throw Error('Content too large');r.verdict=parse(r.raw);if(r.verdict==='error')r.error='invalid_verdict_token'}
 r.usage={};for(const k of ['prompt_tokens','completion_tokens','total_tokens'])if(Number.isSafeInteger(d.usage?.[k]))r.usage[k]=d.usage[k];
 }
 }catch{r.error='transport_or_response_error';r.verdict='error';halt=true}
 results.push(r);fs.writeFileSync(dest+'/'+j.index+'.json',JSON.stringify(r,null,2)+'\n');console.log(JSON.stringify(r));if(halt)break;
 }
 function stats(rs){return {executions:rs.length,matches:rs.filter(x=>x.verdict===x.expected).length,misses:rs.filter(x=>x.expected==='unsupported'&&x.verdict==='clear').length,false_alarms:rs.filter(x=>x.expected==='clear'&&x.verdict==='unsupported').length,uncertain:rs.filter(x=>x.verdict==='uncertain').length,errors:rs.filter(x=>x.verdict==='error').length}}
 const old=JSON.parse(fs.readFileSync(base+'/summary.json'));
 const summary={planned:24,attempted:results.length,all:stats(results),previous:stats(results.filter(x=>x.cohort==='previous')),new_fixed:stats(results.filter(x=>x.cohort==='new_fixed')),total_tokens:results.reduce((n,r)=>n+(r.usage?.total_tokens||0),0),deepseek_previous:old.all,results};
 fs.writeFileSync(dest+'/summary.json',JSON.stringify(summary,null,2)+'\n');console.log(JSON.stringify({done:true,...summary,results:undefined}));})().catch(()=>{console.error('Runner failed; details withheld');process.exitCode=1});
