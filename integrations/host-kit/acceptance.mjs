// Compose existing checks; never turn missing evidence into a pass.
import {fileURLToPath} from 'node:url';
import {resolve} from 'node:path';
import {preflightHost,preflightExitCode} from './preflight.mjs';
import {verifyHost} from './verify.mjs';
const object=v=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const lifecyclePaths=['me','session','messages','receipt','handoff','next','deskMe','queue','action','customerLogin','customerLogout','staffLogin','staffLogout'];
export async function acceptHost(input,{allowTestWrites=false,preflight=preflightHost,verify=verifyHost}={}){
 const checks=[],phases={};
 const add=(id,status,detail)=>checks.push({id,status,detail});
 const finish=()=>({schemaVersion:1,mode:allowTestWrites?'authorized_test_writes':'read_only',status:checks.some(c=>c.status==='fail')?'fail':checks.some(c=>c.status==='needs_verification')?'needs_verification':'pass',checks,phases});
 try{
  if(!object(input)||Object.keys(input).some(k=>!['preflight','lifecycle'].includes(k))||!object(input.preflight))throw Error();
  if(input.lifecycle!==undefined){
   const c=input.lifecycle;
   if(!object(c)||Object.keys(c).some(k=>!['customers','staff','paths'].includes(k))||!Array.isArray(c.customers)||c.customers.length!==2||!c.customers.every(object)||!object(c.staff))throw Error();
   if(c.paths!==undefined&&(!object(c.paths)||Object.entries(c.paths).some(([k,p])=>!lifecyclePaths.includes(k)||typeof p!=='string'||!/^\/[A-Za-z0-9_/-]+$/.test(p)||p.includes('//'))))throw Error();
   // One target, with identical shared read routes in both phases.
   for(const k of ['me','deskMe','queue'])if((c.paths?.[k]??{me:'/host/me',deskMe:'/desk/me',queue:'/desk/queue'}[k])!==(input.preflight.paths?.[k]??{me:'/host/me',deskMe:'/desk/me',queue:'/desk/queue'}[k]))throw Error();
  }
 }catch{add('configuration','fail','输入结构、测试账号或两阶段读取路径不一致；未执行网络请求。');return finish();}
 try{phases.preflight=await preflight(input.preflight);}catch{add('preflight','fail','只读检查未完成；未输出原始异常。');return finish();}
 add('preflight',phases.preflight.status,'只读结果保留在 phases.preflight；缺失会话不等于失败或通过。');
 if(phases.preflight.status==='fail')add('lifecycle','needs_verification','只读检查失败，未执行业务写操作。');
 else if(!allowTestWrites)add('lifecycle','needs_verification','未显式允许测试写入，未登录、发送或注销。');
 else if(!input.lifecycle)add('lifecycle','needs_verification','未提供两个可丢弃客户及坐席的登录配置。');
 else{
  try{phases.lifecycle=await verify({origin:input.preflight.origin,...input.lifecycle},{allowTestWrites:true});add('lifecycle',phases.lifecycle.passed?'pass':'fail','检查人工接待闭环、双客户隔离、旧聊天凭据及退出；注销清理失败也算失败。');}
  catch{add('lifecycle','fail','业务验收异常中断，测试状态与注销清理需人工核对；未输出原始异常。');}
 }
 add('identity_transitions','needs_verification','在目标认证系统触发切号、重新登录、禁用及恢复，分别验证旧 Cookie、聊天凭据和写请求被拒绝；样例回归不能代替目标证据。');
 add('browser','needs_verification','在目标浏览器验收跨标签页退出、迟到响应、Cookie策略及页面清理。');
 add('deployment','needs_verification','本入口仅验证本机 fake 测试环境；持久化、真实模型、HTTPS和生产部署另行验收。');
 return finish();
}
export const acceptanceExitCode=preflightExitCode;
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 try{
  if(process.argv.length>3||process.argv.length===3&&process.argv[2]!=='--allow-test-writes')throw Error();
  let raw='';for await(const chunk of process.stdin){raw+=chunk;if(Buffer.byteLength(raw)>65536)throw Error();}
  const report=await acceptHost(JSON.parse(raw),{allowTestWrites:process.argv[2]==='--allow-test-writes'});
  console.log(JSON.stringify(report));process.exitCode=acceptanceExitCode(report);
 }catch{console.log(JSON.stringify({schemaVersion:1,status:'fail',checks:[{id:'configuration',status:'fail',detail:'输入无效；配置仅从 stdin 读取。'}]}));process.exitCode=1;}
}
