const fs=require('fs'),cp=require('child_process'),crypto=require('crypto');
const root='/Users/LOCAL_USER/Code/GitHub/omni-agent-customer-service';
const binary='/tmp/omni-controlled-20260926',dest=root+'/examples/knowledge/webchat/eval_runs/2026-09-26/controlled-contract-length-v1';
const live=process.argv.includes('--allow-model-api');
if(process.argv.slice(2).some(x=>x!=='--allow-model-api'))throw Error('Invalid arguments');
const hash=x=>crypto.createHash('sha256').update(x).digest('hex');
const source=JSON.parse(fs.readFileSync(root+'/examples/knowledge/webchat/grounding_inventory_cases.json'));
const negative=source.find(x=>x.id==='device-effects'),positive=source.find(x=>x.id==='device-effects-documented');
if(!negative||!positive||negative.answer!==positive.answer)throw Error('Pair changed');
const capability='这类信息需要您通过账号安全相关的功能自行查看，具体入口在现有资料中无法确认，建议您留意官方页面或公告。';
const limitation='您好！很抱歉，我目前没有查询账号登录设备和IP记录的能力，也无法代您执行强制退出所有设备的操作。';
const steps='如果您的顾虑是账号安全，可以参考资料中明确提供的重置密码步骤：\n\n1. 在登录页面点击“忘记密码”；\n2. 使用注册邮箱完成验证；\n3. 设置新密码。';
for(const piece of [capability,limitation,steps])if(!negative.answer.includes(piece))throw Error('Not a literal slice');
const cases=[{...negative,id:'full-unsupported'},{...positive,id:'full-supported'},
{...negative,id:'capability-unsupported',answer:capability},
{...positive,id:'capability-supported',answer:capability},
{...positive,id:'assistant-limitation',answer:limitation,expected:'clear',rationale:'仅助手限制，不断言业务能力。'},
{...positive,id:'supported-steps',answer:steps,expected:'clear',rationale:'原资料明确支持密码重置步骤，保留标题和编号。'}].map(x=>({...x,origin:'controlled-contract-length-v1; literal slices of frozen device answer'}));
const plans={};
for(const mode of ['typed','split']){const p=cp.spawnSync(binary,['-'+mode+'-primary','-cases',root+'/examples/knowledge/webchat/grounding_inventory_cases.json'],{cwd:root,encoding:'utf8',env:{PATH:process.env.PATH}});if(p.status!==0)throw Error('Invalid offline plan');plans[mode]=JSON.parse(p.stdout);}
if(plans.typed.review_prompt!==plans.split.review_prompt)throw Error('Different reviewers');
const jobs=[];
for(let round=1;round<=2;round++)for(let c=0;c<cases.length;c++)for(const mode of round===1?['typed','split']:['split','typed'])jobs.push({index:jobs.length,round,case_index:c,id:cases[c].id,mode});
const sources={};for(const file of fs.readdirSync(root+'/cmd/grounding-check').filter(x=>x.endsWith('.go')).sort())sources[file]=hash(fs.readFileSync(root+'/cmd/grounding-check/'+file));
const freeze={started_at:new Date().toISOString(),max_provider_requests:48,rounds:2,no_retries:true,model:'glm-5.3-flash',reasoning_effort:'low',review_prompt_sha256:hash(plans.typed.review_prompt),primary_prompt_sha256:{typed:hash(plans.typed.prompt),split:hash(plans.split.prompt)},source_hashes:sources,binary_sha256:hash(fs.readFileSync(binary)),runner_sha256:hash(fs.readFileSync(__filename)),cases,jobs};
if(!live){console.log(JSON.stringify({offline:true,jobs:jobs.length,max_provider_requests:48,same_reviewer:true,cases:cases.map(x=>({id:x.id,expected:x.expected,characters:x.answer.length}))},null,2));process.exit(0)}
const env={PATH:process.env.PATH,GROUNDING_REASONING_EFFORT:'low'},allowed=new Set(['AI_PROVIDER','AI_PROTOCOL','AI_MODEL','AI_BASE_URL','AI_API_KEY','ZHIPUAI_API_KEY']);
for(const line of fs.readFileSync(root+'/.env.local','utf8').split(/\r?\n/)){const m=line.match(/^\s*(?:export\s+)?([A-Z_]+)\s*=\s*(.*?)\s*$/);if(!m||!allowed.has(m[1]))continue;let v=m[2];if(v.includes('${')||v.includes('`'))throw Error('Dynamic env unsupported');if((v.startsWith('"')&&v.endsWith('"'))||(v.startsWith("'")&&v.endsWith("'")))v=v.slice(1,-1);else v=v.replace(/\s+#.*$/,'');env[m[1]]=v;}
if(env.AI_PROVIDER!=='glm'||env.AI_PROTOCOL!=='chat_completions'||env.AI_MODEL!==freeze.model)throw Error('Unexpected model configuration');
if(fs.existsSync(dest))throw Error('Refusing repeated run');
fs.mkdirSync(dest,{recursive:true});
fs.writeFileSync(dest+'/freeze.json',JSON.stringify(freeze,null,2)+'\n');
fs.copyFileSync(__filename,dest+'/runner.cjs');
for(let i=0;i<cases.length;i++)fs.writeFileSync(dest+'/case-'+i+'.json',JSON.stringify([cases[i]],null,2)+'\n');
const results=[];
async function run(j){await new Promise((resolve,reject)=>{const child=cp.spawn(binary,['-'+j.mode+'-primary','-cases',dest+'/case-'+j.case_index+'.json','-allow-model-api','-timeout','3m'],{cwd:root,env,stdio:['ignore','pipe','pipe']});let out='',progress='';child.stdout.on('data',d=>out+=d);child.stderr.on('data',d=>progress+=d);child.on('error',reject);child.on('close',code=>{fs.writeFileSync(dest+'/'+j.index+'.json',out);fs.writeFileSync(dest+'/'+j.index+'-progress.txt',progress);let item={...j,exit_code:code};try{const r=JSON.parse(out),v=r.results[0];if(hash(r.prompt)!==freeze.primary_prompt_sha256[j.mode]||hash(r.review_prompt)!==freeze.review_prompt_sha256)throw Error('Prompt changed');Object.assign(item,{requests:r.provider_requests,expected:v?.expected,verdict:v?.verdict,error:v?.error,failure:v?.failure,complete:r.complete});}catch{item.invalid_report=true}results.push(item);console.log(JSON.stringify(item));resolve()})})}
(async()=>{for(const j of jobs)await run(j);for(const [f,h]of Object.entries(sources))if(hash(fs.readFileSync(root+'/cmd/grounding-check/'+f))!==h)throw Error('Frozen source changed');const summary={source_hashes_verified:true,requests:results.reduce((n,r)=>n+(r.requests||0),0),matches:results.filter(r=>r.expected&&r.expected===r.verdict).length,results};fs.writeFileSync(dest+'/summary.json',JSON.stringify(summary,null,2)+'\n');console.log(JSON.stringify({done:true,requests:summary.requests,matches:summary.matches}))})().catch(()=>{console.error('Runner failed');process.exitCode=1});
