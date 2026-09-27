const fs=require('fs'),assert=require('assert/strict'),crypto=require('crypto');
const root='/Users/LOCAL_USER/Code/GitHub/omni-agent-customer-service',dest=root+'/examples/knowledge/webchat/eval_runs/2026-09-27/deepseek-strict-probe-v1';
const object=properties=>({type:'object',properties,required:Object.keys(properties),additionalProperties:false});
const cases=[
 {id:'flat-enum',schema:object({status:{type:'string',enum:['ok']}}),requested:{status:'forbidden',extra:true},valid:{status:'ok'}},
 {id:'required-integer',schema:object({count:{type:'integer',minimum:1,maximum:3},label:{type:'string'}}),requested:{count:9,extra:true},valid:{count:2,label:'test'}},
 {id:'nested-anyof',schema:object({items:{type:'array',items:{anyOf:[object({kind:{type:'string',enum:['fact']},evidence:{type:'string'}}),object({kind:{type:'string',enum:['other']},reason:{type:'string'}})]}}}),requested:{items:[{kind:'other',reason:'test',evidence:'SKIP',reason_note:''}]},valid:{items:[{kind:'other',reason:'test'}]}}
];
// Small validator only for the exact schema vocabulary used by these probes.
// Reject unknown schema keywords so a missing implementation cannot look valid.
function validate(s,v,p='$'){
 const allowed=new Set(['type','properties','required','additionalProperties','enum','minimum','maximum','items','anyOf']);
 for(const k of Object.keys(s))if(!allowed.has(k))throw Error('Unsupported validator keyword');
 if(s.anyOf){return s.anyOf.some(x=>validate(x,v,p).length===0)?[]:[p+': no anyOf branch matches'];}
 const e=[];
 if(s.type==='object'){
  if(!v||typeof v!=='object'||Array.isArray(v))return [p+': expected object'];
  for(const k of s.required)if(!Object.hasOwn(v,k))e.push(p+'.'+k+': missing');
  for(const k of Object.keys(v)){if(!Object.hasOwn(s.properties,k)){if(s.additionalProperties===false)e.push(p+'.'+k+': extra');}else e.push(...validate(s.properties[k],v[k],p+'.'+k));}
 }else if(s.type==='array'){
  if(!Array.isArray(v))return [p+': expected array'];v.forEach((x,i)=>e.push(...validate(s.items,x,p+'['+i+']')));
 }else if(s.type==='integer'){
  if(!Number.isInteger(v))return [p+': expected integer'];if(v<s.minimum||v>s.maximum)e.push(p+': outside range');
 }else if(s.type==='string'){if(typeof v!=='string')e.push(p+': expected string');}
 else throw Error('Unsupported schema type');
 if(s.enum&&!s.enum.includes(v))e.push(p+': outside enum');return e;
}
for(const c of cases){assert.deepEqual(validate(c.schema,c.valid),[]);assert.ok(validate(c.schema,c.requested).length>0)}
assert.ok(validate(cases[1].schema,{count:1.5,label:'test'}).length);assert.ok(validate(cases[1].schema,{count:1,label:null}).length);
assert.deepEqual(validate(cases[2].schema,{items:[{kind:'fact',evidence:'x'}]}),[]);
assert.ok(validate(cases[2].schema,{items:[{kind:'fact',reason:'x'}]}).length);
assert.ok(validate(cases[2].schema,{items:[{kind:'other',reason:'x',evidence:null}]}).length);
const jobs=[];for(let round=1;round<=2;round++)for(const c of cases)for(const strict of round===1?[false,true]:[true,false])jobs.push({index:jobs.length,round,case_id:c.id,strict});
const payload=c=>JSON.stringify(c.requested);
const makeBody=(c,strict)=>({model:'deepseek-flash',stream:false,max_tokens:512,thinking:{type:'disabled'},messages:[{role:'system',content:'This is a synthetic API schema diagnostic, not a real action. Call submit_probe once. Copy the user-provided JSON as the tool arguments exactly, including extra fields and enum values; do not fix it. No tool will be executed.'},{role:'user',content:payload(c)}],tools:[{type:'function',function:{name:'submit_probe',description:'Submit diagnostic data only. No action is executed.',strict,parameters:c.schema}}],tool_choice:{type:'function',function:{name:'submit_probe'}}});
if(!process.argv.includes('--allow-model-api')){console.log(JSON.stringify({offline_tests:'passed',planned:jobs.length,max_requests:12,max_tokens_per_request:512,cases:cases.map(x=>x.id)}));process.exit(0)}
const keys=[];for(const line of fs.readFileSync(root+'/.env.local','utf8').split(/\r?\n/)){const m=line.match(/^\s*(?:export\s+)?DEEPSEEK_API_KEY\s*=\s*(.*?)\s*$/);if(!m)continue;let v=m[1];if((v.startsWith('"')&&v.endsWith('"'))||(v.startsWith("'")&&v.endsWith("'")))v=v.slice(1,-1);else v=v.replace(/\s+#.*$/,'');keys.push(v)}
if(keys.length!==1||!keys[0]||keys[0].includes('${')||keys[0].includes('`'))throw Error('Missing or ambiguous key');
if(fs.existsSync(dest))throw Error('Refusing duplicate run');fs.mkdirSync(dest,{recursive:true});
const endpoint='https://api.deepseek.com/beta/chat/completions';
fs.writeFileSync(dest+'/freeze.json',JSON.stringify({started_at:new Date().toISOString(),endpoint,max_requests:12,no_retries:true,runner_sha256:crypto.createHash('sha256').update(fs.readFileSync(__filename)).digest('hex'),jobs:jobs.map(j=>({...j,request:makeBody(cases.find(c=>c.id===j.case_id),j.strict)}))},null,2)+'\n');fs.copyFileSync(__filename,dest+'/runner.cjs');
const results=[];
(async()=>{for(const j of jobs){const c=cases.find(x=>x.id===j.case_id),start=Date.now();const item={...j};let halt=false;
 try{const response=await fetch(endpoint,{method:'POST',redirect:'error',signal:AbortSignal.timeout(75000),headers:{Authorization:'Bearer '+keys[0],'Content-Type':'application/json'},body:JSON.stringify(makeBody(c,j.strict))});item.http_status=response.status;
  const text=await response.text();if(Buffer.byteLength(text)>1048576)throw Error('Response limit');let d;try{d=JSON.parse(text)}catch{}
  if(!response.ok){item.error='http_rejection';halt=[401,403,429].includes(response.status);}else if(!d){item.error='invalid_envelope';}else{
   item.returned_model=typeof d.model==='string'&&/^[a-zA-Z0-9._:-]{1,80}$/.test(d.model)?d.model:'unreported';const choice=d.choices?.[0],calls=choice?.message?.tool_calls;
   item.finish_reason=['stop','length','tool_calls'].includes(choice?.finish_reason)?choice.finish_reason:'other';
   if(d.choices?.length!==1||choice.finish_reason!=='tool_calls'||calls?.length!==1||calls[0].type!=='function'||calls[0].function?.name!=='submit_probe'){item.error='unexpected_tool_envelope';}
   else{const raw=calls[0].function.arguments;if(typeof raw!=='string'||raw.length>32768)throw Error('Invalid arguments');item.arguments_raw=raw;try{const value=JSON.parse(raw);item.violations=validate(c.schema,value);item.schema_valid=item.violations.length===0;}catch{item.error='invalid_arguments_json'}}
   item.usage={};for(const k of ['prompt_tokens','completion_tokens','total_tokens'])if(Number.isSafeInteger(d.usage?.[k]))item.usage[k]=d.usage[k];
  }
 }catch{item.error='transport_or_response_error';halt=true}
 item.elapsed_ms=Date.now()-start;results.push(item);fs.writeFileSync(dest+'/'+j.index+'.json',JSON.stringify(item,null,2)+'\n');console.log(JSON.stringify(item));if(halt)break;
 }
 const summary={planned:12,attempted:results.length,results,groups:[false,true].map(strict=>{const r=results.filter(x=>x.strict===strict);return {strict,attempted:r.length,valid:r.filter(x=>x.schema_valid===true).length,violations:r.filter(x=>x.schema_valid===false).length,errors:r.filter(x=>x.error).length}}),total_tokens:results.reduce((n,x)=>n+(x.usage?.total_tokens||0),0)};
 fs.writeFileSync(dest+'/summary.json',JSON.stringify(summary,null,2)+'\n');console.log(JSON.stringify({done:true,groups:summary.groups,total_tokens:summary.total_tokens}));
})().catch(()=>{console.error('Probe failed; details withheld');process.exitCode=1});
