const fs=require('fs'),assert=require('assert/strict'),crypto=require('crypto');
const root='/Users/LOCAL_USER/Code/GitHub/omni-agent-customer-service';
const base=root+'/examples/knowledge/webchat/eval_runs/2026-09-27/deepseek-verdict-only-v1/freeze.json';
const dest=root+'/examples/knowledge/webchat/eval_runs/2026-09-27/glm-boundary-four-v1';
const bytes=fs.readFileSync(base),prior=JSON.parse(bytes),hash=x=>crypto.createHash('sha256').update(x).digest('hex');
assert.equal(hash(prior.prompt),prior.prompt_sha256);
const source=prior.cases.find(c=>c.id==='capability-unsupported');assert(source);
const cases=[
 {id:'explicit-capability',answer:'本系统支持您通过账号安全相关的功能自行查看最近登录设备和IP记录，具体入口在现有资料中无法确认，建议您留意官方页面或公告。',expected:'unsupported'},
 {id:'implicit-original',answer:source.answer,expected:'unsupported'},
 {id:'consult-whether',answer:'建议您向官方咨询是否支持查看最近登录设备和IP记录。',expected:'clear'},
 {id:'explicit-unknown',answer:'现有资料未说明是否支持查看最近登录设备和IP记录，我无法确认。',expected:'clear'}
].map(c=>({...c,reference:source.reference,history:source.history}));
const parse=x=>new Map([['有','unsupported'],['无','clear'],['无法判断','uncertain']]).get(typeof x==='string'?x.trim():'')||'error';
for(const [x,y]of [['有','unsupported'],[' 无\n','clear'],['无法判断','uncertain'],['有。','error'],['{}','error'],['toString','error'],['','error']])assert.equal(parse(x),y);
assert.equal(cases.length,4);assert.equal(cases[1].answer,source.answer);
const endpoint='https://open.bigmodel.cn/api/paas/v4/chat/completions',model='glm-5.3-flash';
const payload=c=>({model,stream:false,thinking:{type:'enabled'},reasoning_effort:'low',max_tokens:4096,messages:[{role:'system',content:prior.prompt},{role:'user',content:JSON.stringify({reference:c.reference,history:c.history,answer:c.answer})}]});
for(const c of cases)assert.deepEqual(Object.keys(JSON.parse(payload(c).messages[1].content)),['reference','history','answer']);
if(!process.argv.includes('--allow-model-api')){console.log(JSON.stringify({offline_tests:'passed',planned:4,model}));process.exit(0)}
const keys=[];for(const line of fs.readFileSync(root+'/.env.local','utf8').split(/\r?\n/)){const m=line.match(/^\s*(?:export\s+)?ZHIPUAI_API_KEY\s*=\s*(.*?)\s*$/);if(!m)continue;let v=m[1];if((v.startsWith('"')&&v.endsWith('"'))||(v.startsWith("'")&&v.endsWith("'")))v=v.slice(1,-1);else v=v.replace(/\s+#.*$/,'');keys.push(v)}
if(keys.length!==1||!keys[0]||keys[0].includes('${')||keys[0].includes('`'))throw Error('Missing or ambiguous provider key');
if(fs.existsSync(dest))throw Error('Refusing duplicate run');fs.mkdirSync(dest,{recursive:true});
fs.writeFileSync(dest+'/freeze.json',JSON.stringify({started_at:new Date().toISOString(),endpoint,model,thinking:'enabled',reasoning_effort:'low',max_tokens:4096,max_requests:4,no_retries:true,prompt:prior.prompt,prompt_sha256:hash(prior.prompt),baseline_sha256:hash(bytes),runner_sha256:hash(fs.readFileSync(__filename)),cases},null,2)+'\n');fs.copyFileSync(__filename,dest+'/runner.cjs');
(async()=>{const results=[];for(const [index,c]of cases.entries()){const r={index,id:c.id,expected:c.expected};let halt=false;try{
const resp=await fetch(endpoint,{method:'POST',redirect:'error',signal:AbortSignal.timeout(75000),headers:{Authorization:'Bearer '+keys[0],'Content-Type':'application/json'},body:JSON.stringify(payload(c))});r.http_status=resp.status;
if(!resp.ok){r.verdict='error';r.error='http_rejection';halt=true;await resp.body?.cancel()}else{const d=await resp.json(),ch=d.choices?.[0];r.finish_reason=['stop','length','tool_calls'].includes(ch?.finish_reason)?ch.finish_reason:'other';r.returned_model=typeof d.model==='string'&&/^[a-zA-Z0-9._:-]{1,80}$/.test(d.model)?d.model:'unreported';
if(d.choices?.length!==1||ch?.finish_reason!=='stop'||typeof ch?.message?.content!=='string'||ch.message.tool_calls?.length||ch.message.content.length>32768){r.verdict='error';r.error='invalid_response'}else{r.raw=ch.message.content;r.verdict=parse(r.raw)}
r.usage={};for(const k of ['prompt_tokens','completion_tokens','total_tokens'])if(Number.isSafeInteger(d.usage?.[k]))r.usage[k]=d.usage[k];}
}catch{r.verdict='error';r.error='transport_or_response_error';halt=true}
results.push(r);fs.writeFileSync(dest+'/'+index+'.json',JSON.stringify(r,null,2)+'\n');console.log(JSON.stringify(r));if(halt)break;}
const summary={planned:4,attempted:results.length,matches:results.filter(r=>r.verdict===r.expected).length,errors:results.filter(r=>r.verdict==='error').length,total_tokens:results.reduce((n,r)=>n+(r.usage?.total_tokens||0),0),results};fs.writeFileSync(dest+'/summary.json',JSON.stringify(summary,null,2)+'\n');console.log(JSON.stringify({done:true,...summary}));})().catch(()=>{console.error('Runner failed; details withheld');process.exitCode=1});
