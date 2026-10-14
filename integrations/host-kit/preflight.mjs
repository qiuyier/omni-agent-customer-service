// Read-only, loopback-only onboarding diagnostics. Never logs in or mutates a conversation.
import {fileURLToPath} from 'node:url';
import {resolve} from 'node:path';
const roles=['customer','staff'];
const slots=['customer','staff','revokedCustomer','revokedStaff','unassignedStaff'];
const fixes={config:'检查输入配置：仅允许本机回环地址、同源只读路径和已有测试会话。',identity:'检查业务身份接口的字段转换、角色和当前会话验证。',proxy:'检查业务代理路由、Host/Origin转换及上游服务状态。',authorization:'检查服务端坐席映射和角色隔离，不能只验证员工是否登录。',privacy:'检查错误脱敏和响应字段白名单；不要返回密码、令牌或内部异常。',cache:'为身份与会话读取接口设置 Cache-Control: no-store。',manual:'使用授权测试账号执行专门验收；只读检查不能证明状态转换或浏览器行为。'};
const object=value=>value!==null&&typeof value==='object'&&!Array.isArray(value);
const safeID=value=>typeof value==='string'&&/^[A-Za-z0-9_-]{1,128}$/.test(value);
function validate(input){
 if(!object(input)||Object.keys(input).some(k=>!['origin','paths','sessions'].includes(k)))throw Error();
 const url=new URL(input.origin);
 if(url.protocol!=='http:'||url.hostname!=='127.0.0.1'||!url.port||url.pathname!=='/'||url.username||url.password||url.search||url.hash)throw Error();
 if(!object(input.paths)||Object.keys(input.paths).some(k=>!['customerIdentity','staffIdentity','me','deskMe','queue'].includes(k)))throw Error();
 const paths={me:'/host/me',deskMe:'/desk/me',queue:'/desk/queue',...input.paths};
 for(const path of Object.values(paths))if(typeof path!=='string'||!/^\/[A-Za-z0-9_/-]+$/.test(path)||path.includes('//'))throw Error();
 if(!paths.customerIdentity||!paths.staffIdentity||new Set(Object.values(paths)).size!==5)throw Error();
 const sessions=input.sessions??{};
 if(!object(sessions)||Object.keys(sessions).some(k=>!slots.includes(k)))throw Error();
 for(const value of Object.values(sessions)){
  if(!object(value)||Object.keys(value).some(k=>!['cookie','expectedID'].includes(k))||typeof value.cookie!=='string'||value.cookie.length>4096||!/^[-A-Za-z0-9_]+=[A-Za-z0-9_.~-]+(?:; [-A-Za-z0-9_]+=[A-Za-z0-9_.~-]+)*$/.test(value.cookie)||!safeID(value.expectedID))throw Error();
 }
 return {origin:url.origin,paths,sessions};
}
function identity(data,role){
 if(!object(data)||Object.keys(data).length!==1||!Object.hasOwn(data,'identity'))return false;
 const i=data.identity;
 return i===null||object(i)&&Object.keys(i).sort().join(',')==='id,role,version'&&safeID(i.id)&&i.role===role&&typeof i.version==='string'&&/^[A-Za-z0-9_-]{16,128}$/.test(i.version);
}
function sanitizedError(data){
 return object(data)&&Object.keys(data).length===1&&['code','error'].some(k=>typeof data[k]==='string'&&/^[a-z][a-z0-9_]{0,63}$/.test(data[k]));
}
function suspicious(data,secrets,depth=0){
 if(depth>32)return true;
 if(typeof data==='string')return secrets.some(s=>data.includes(s))||/\bat\s+\S+\s+\([^\n]+:\d+:\d+\)/.test(data);
 if(!data||typeof data!=='object')return false;
 return Object.entries(data).some(([key,value])=>/^(password|passphrase|secret|.*_secret|access_token|refresh_token|authorization|cookie|sessionid|session_id|ticket|stack|trace)$/i.test(key)||suspicious(value,secrets,depth+1));
}
export async function preflightHost(input,{fetcher=globalThis.fetch,timeoutMs=5000}={}){
 const checks=[];
 const add=(id,status,detail,area)=>checks.push({id,status,detail,...(area?{fix:fixes[area]}:{})});
 const finish=()=>({schemaVersion:1,mode:'read_only',status:checks.some(c=>c.status==='fail')?'fail':checks.some(c=>c.status==='needs_verification')?'needs_verification':'pass',checks});
 let config;
 try{config=validate(input);if(!Number.isInteger(timeoutMs)||timeoutMs<1||timeoutMs>30000)throw Error();}catch{add('configuration','fail','输入不符合只读检查契约。','config');return finish();}
 add('configuration','pass','回环地址、只读路径与输入结构有效。');
 const {origin,paths,sessions}=config;
 const secrets=Object.values(sessions).flatMap(s=>s.cookie.split('; ').map(pair=>pair.slice(pair.indexOf('=')+1))).filter(value=>value.length>=8);
 async function read(id,path,session,version){
  try{
   const response=await fetcher(origin+path,{method:'GET',headers:{Accept:'application/json',Origin:origin,...(session?{Cookie:session.cookie}:{}),...(version?{'X-Business-Session':version}:{})},redirect:'error',signal:AbortSignal.timeout(timeoutMs)});
   if(response.status>=300&&response.status<400)throw Error();
   const reader=response.body?.getReader();let size=0;const chunks=[];
   if(reader)try{while(true){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>65536)throw Error();chunks.push(Buffer.from(value));}}finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
   if(response.headers.get('content-type')?.split(';')[0].trim().toLowerCase()!=='application/json')throw Error();
   const data=JSON.parse(Buffer.concat(chunks).toString('utf8'));
   add(id+'.transport','pass','只读请求取得有界JSON响应。');
   checks.at(-1).httpStatus=response.status;
   const cache=(response.headers.get('cache-control')||'').toLowerCase().split(',').map(s=>s.trim());
   add(id+'.cache',cache.includes('no-store')?'pass':'fail',cache.includes('no-store')?'响应声明禁止缓存。':'身份或会话响应未声明禁止缓存。','cache');
   const privateData=suspicious(data,secrets)||(response.status>=400&&!sanitizedError(data));
   add(id+'.privacy',privateData?'fail':'pass',privateData?'发现疑似敏感字段或未规范化错误响应。':'未发现已知敏感字段、已提供的长会话值或明显堆栈。','privacy');
   return {status:response.status,data};
  }catch{add(id+'.transport','fail','请求超时、重定向、网络失败、响应过大或不是有效JSON；未输出原始错误。','proxy');return null;}
 }
 const check=(id,ok,detail,area)=>add(id,ok?'pass':'fail',detail,area);
 async function publicIdentity(prefix,role,session,expected){
  const response=await read(prefix,paths[role+'Identity'],session);if(!response)return null;
  const valid=response.status===200&&identity(response.data,role);
  check(prefix+'.schema',valid,'检查公开身份格式及角色。','identity');
  if(valid)check(prefix+'.binding',expected===null?response.data.identity===null:response.data.identity?.id===expected,'检查身份是否与预期登录状态匹配。','identity');
  return valid?response.data.identity:null;
 }
 for(const role of roles)await publicIdentity('anonymous.'+role,role,null,null);
 const meta=await read('anonymous.host',paths.me);
 if(meta)check('anonymous.host.contract',meta.status===200&&meta.data?.user===null&&meta.data?.demo===false&&['fake','ai'].includes(meta.data?.reply_mode)&&['memory','postgres'].includes(meta.data?.storage)&&typeof meta.data?.human_enabled==='boolean','检查宿主元数据、匿名身份和非演示认证模式。','proxy');
 const desk=await read('anonymous.desk',paths.deskMe);
 if(desk)check('anonymous.desk.denied',desk.status===200&&desk.data?.agent===null,'匿名请求不应取得坐席身份。','authorization');
 const denied=async(id,session)=>{const r=await read(id,paths.queue,session);if(r)check(id+'.denied',[401,403].includes(r.status),'该身份不应读取客服队列。','authorization');};
 await denied('anonymous.queue');
 for(const role of roles){
  const session=sessions[role];
  if(!session){add(role+'.session','needs_verification','未提供已登录测试会话，无法检查该角色的实际身份和授权。','manual');continue;}
  const who=await publicIdentity(role+'.identity',role,session,session.expectedID);
  await publicIdentity(role+'.opposite_identity',role==='customer'?'staff':'customer',session,null);
  const r=await read(role+'.binding',paths[role==='customer'?'me':'deskMe'],session);
  if(r)check(role+'.binding.expected',r.status===200&&r.data?.[role==='customer'?'user':'agent']?.id===session.expectedID,'检查业务身份与宿主用户或授权坐席映射一致。','authorization');
  if(role==='customer')await denied('customer.queue',session);
  else{const queue=await read('staff.queue',paths.queue,session);if(queue)check('staff.queue.allowed',queue.status===200&&Array.isArray(queue.data?.items),'检查已授权坐席能读取队列及上游连通性；不输出队列正文。','proxy');}
  if(who){const path=paths[role==='customer'?'me':'deskMe'];
   // Only read routes are probed. Header policy and write-route enforcement remain separate.
   const current=await read(role+'.current_version',path,session,who.version);
   const stale=await read(role+'.stale_version',path,session,who.version+'-invalid');
   if(current)check(role+'.current_version.accepted',current.status===200&&current.data?.[role==='customer'?'user':'agent']?.id===session.expectedID,'当前公开版本应与当前登录匹配。','identity');
   if(stale)check(role+'.stale_version.denied',[401,403].includes(stale.status),'错误的公开版本应被业务代理拒绝。','identity');
  }
 }
 for(const [slot,role] of [['revokedCustomer','customer'],['revokedStaff','staff']]){
  const session=sessions[slot];if(!session){add(slot,'needs_verification','未提供已在外部撤销的测试会话；工具不会自行退出或禁用账号。','manual');continue;}
  await publicIdentity(slot+'.identity',role,session,null);
  const r=await read(slot+'.binding',paths[role==='customer'?'me':'deskMe'],session);
  if(r)check(slot+'.denied',[401,403].includes(r.status)||r.status===200&&r.data?.[role==='customer'?'user':'agent']===null,'外部声明已撤销的会话不应恢复身份；未在此触发撤销操作。','identity');
  await denied(slot+'.queue',session);
 }
 if(sessions.unassignedStaff){await publicIdentity('unassigned.identity','staff',sessions.unassignedStaff,sessions.unassignedStaff.expectedID);const r=await read('unassigned.desk',paths.deskMe,sessions.unassignedStaff);if(r)check('unassigned.desk.denied',r.status===200&&r.data?.agent===null||[401,403].includes(r.status),'有员工登录但无坐席映射时不应获得客服权限。','authorization');await denied('unassigned.queue',sessions.unassignedStaff);}
 else add('unassignedStaff','needs_verification','未提供未授权员工测试会话，无法检查员工登录与坐席授权是否分离。','manual');
 add('state_transitions','needs_verification','未触发登录、退出、禁用、发送、接管、已读或关闭；状态转换和写接口权限需显式验收。','manual');
 add('browser_and_privacy','needs_verification','Cookie属性、标签页清理、HTTPS及未知敏感信息泄露需浏览器与安全审查；本工具不是完整安全扫描。','manual');
 return finish();
}
export const preflightExitCode=report=>report.status==='pass'?0:report.status==='needs_verification'?2:1;
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 try{if(process.argv.length!==2)throw Error();let raw='';for await(const chunk of process.stdin){raw+=chunk;if(Buffer.byteLength(raw)>65536)throw Error();}const result=await preflightHost(JSON.parse(raw));console.log(JSON.stringify(result));process.exitCode=preflightExitCode(result);}
 catch{console.log(JSON.stringify({schemaVersion:1,mode:'read_only',status:'fail',checks:[{id:'configuration',status:'fail',detail:'输入无效；只从stdin读取配置，不接受命令行凭据。',fix:fixes.config}]}));process.exitCode=1;}
}
