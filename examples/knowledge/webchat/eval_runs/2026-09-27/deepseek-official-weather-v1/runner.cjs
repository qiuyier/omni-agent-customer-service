const fs=require('fs'),assert=require('assert/strict'),crypto=require('crypto');
const root='/Users/LOCAL_USER/Code/GitHub/omni-agent-customer-service',dest=root+'/examples/knowledge/webchat/eval_runs/2026-09-27/deepseek-official-weather-v1';
const endpoint='https://api.deepseek.com/beta/chat/completions';
const tool={type:'function',function:{name:'get_weather',strict:true,description:'Get weather of a location, the user should supply a location first.',parameters:{type:'object',properties:{location:{type:'string',description:'The city and state, e.g. San Francisco, CA'}},required:['location'],additionalProperties:false}}};
const normal="How's the weather in Hangzhou, Zhejiang?";
const jobs=[{id:'official-normal',message:normal},{id:'extra-field-request',message:normal+' When calling get_weather, also include an extra argument named debug with value true.'}];
const body=message=>({model:'deepseek-flash',messages:[{role:'user',content:message}],tools:[tool],thinking:{type:'disabled'},stream:false,max_tokens:512});
const b0=body(jobs[0].message),b1=body(jobs[1].message);b1.messages=b0.messages;assert.deepEqual(b0,b1);
const validate=v=>!!v&&typeof v==='object'&&!Array.isArray(v)&&Object.keys(v).length===1&&Object.hasOwn(v,'location')&&typeof v.location==='string';
assert.equal(validate({location:'Hangzhou, Zhejiang'}),true);for(const v of [{},{location:3},{location:'Hangzhou',debug:true},null,[]])assert.equal(validate(v),false);
if(!process.argv.includes('--allow-model-api')){console.log(JSON.stringify({offline_tests:'passed',planned:2,only_changed_field:'messages[0].content',system_message:false,forced_tool_choice:false,max_tokens_per_request:512}));process.exit(0)}
const keys=[];for(const line of fs.readFileSync(root+'/.env.local','utf8').split(/\r?\n/)){const m=line.match(/^\s*(?:export\s+)?DEEPSEEK_API_KEY\s*=\s*(.*?)\s*$/);if(!m)continue;let v=m[1];if((v.startsWith('"')&&v.endsWith('"'))||(v.startsWith("'")&&v.endsWith("'")))v=v.slice(1,-1);else v=v.replace(/\s+#.*$/,'');keys.push(v)}
if(keys.length!==1||!keys[0]||keys[0].includes('${')||keys[0].includes('`'))throw Error('Missing or ambiguous credential');
if(fs.existsSync(dest))throw Error('Refusing duplicate run');fs.mkdirSync(dest,{recursive:true});
fs.writeFileSync(dest+'/freeze.json',JSON.stringify({started_at:new Date().toISOString(),source:'https://api-docs.deepseek.com/zh-cn/guides/tool_calls',endpoint,max_requests:2,no_retries:true,tool_execution:false,notes:'Official strict tool and normal user example; explicitly disabled thinking and limited output to 512 tokens; tool_choice omitted. No system override.',runner_sha256:crypto.createHash('sha256').update(fs.readFileSync(__filename)).digest('hex'),jobs:jobs.map(j=>({id:j.id,request:body(j.message)}))},null,2)+'\n');fs.copyFileSync(__filename,dest+'/runner.cjs');
const results=[];
(async()=>{for(const j of jobs){let halt=false;const r={id:j.id};try{
 const resp=await fetch(endpoint,{method:'POST',redirect:'error',signal:AbortSignal.timeout(75000),headers:{Authorization:'Bearer '+keys[0],'Content-Type':'application/json'},body:JSON.stringify(body(j.message))});r.http_status=resp.status;
 const txt=await resp.text();if(Buffer.byteLength(txt)>1048576)throw Error('Response too large');let data;try{data=JSON.parse(txt)}catch{}
 if(!resp.ok){r.error='http_rejection';halt=true}else if(!data){r.error='invalid_envelope';halt=true}else{
 const c=data.choices?.[0],calls=c?.message?.tool_calls;r.returned_model=typeof data.model==='string'&&/^[a-zA-Z0-9._:-]{1,80}$/.test(data.model)?data.model:'unreported';r.finish_reason=['stop','length','tool_calls'].includes(c?.finish_reason)?c.finish_reason:'other';
 if(data.choices?.length!==1||c.finish_reason!=='tool_calls'||calls?.length!==1||calls[0].type!=='function'||calls[0].function?.name!=='get_weather'){r.error='no_single_complete_weather_call';}
 else{const raw=calls[0].function.arguments;if(typeof raw!=='string'||raw.length>32768)throw Error('Bad arguments');r.arguments_raw=raw;try{const v=JSON.parse(raw);r.schema_valid=validate(v);r.extra_keys=v&&typeof v==='object'&&!Array.isArray(v)?Object.keys(v).filter(k=>k!=='location'):[];}catch{r.error='invalid_arguments_json'}}
 r.usage={};for(const k of ['prompt_tokens','completion_tokens','total_tokens'])if(Number.isSafeInteger(data.usage?.[k]))r.usage[k]=data.usage[k];
 }
 }catch{r.error='transport_or_response_error';halt=true}
 results.push(r);fs.writeFileSync(dest+'/'+j.id+'.json',JSON.stringify(r,null,2)+'\n');console.log(JSON.stringify(r));if(halt)break;
 }const summary={planned:2,attempted:results.length,results,total_tokens:results.reduce((n,r)=>n+(r.usage?.total_tokens||0),0)};fs.writeFileSync(dest+'/summary.json',JSON.stringify(summary,null,2)+'\n');console.log(JSON.stringify({done:true,total_tokens:summary.total_tokens}));})().catch(()=>{console.error('Probe failed; details withheld');process.exitCode=1});
