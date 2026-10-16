import {randomBytes} from 'node:crypto';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {startBusiness} from './business.mjs';
import {attachSupport} from './support.mjs';
export async function createFixture({serveUI=false}={}){
 const random=()=>randomBytes(32).toString('base64url');
 const accounts=[{realm:'member',handle:'lin',number:'MEM:731',passphrase:random()},{realm:'member',handle:'zhou',number:'MEM:864',passphrase:random()},{realm:'operator',handle:'staff',number:'OPS:42',passphrase:random()}];
 const root=fileURLToPath(new URL('../../',import.meta.url)),dir=await mkdtemp(join(tmpdir(),'omni-fresh-'));
 let child,closed,business,support;const hostSecret=random(),agentSecret=random();
 const close=async()=>{await business?.close();business=null;await support?.close();support=null;if(child&&child.exitCode===null&&child.signalCode===null){child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}await rm(dir,{recursive:true,force:true});};
 try{
  const binary=join(dir,'gateway');await promisify(execFile)('go',['build','-o',binary,'./integrations/cross-system/gateway'],{cwd:root,timeout:120000});
  child=spawn(binary,[],{env:{FIXTURE_HOST_A:random(),FIXTURE_AGENT_A:random(),FIXTURE_HOST_B:hostSecret,FIXTURE_AGENT_B:agentSecret},stdio:['ignore','pipe','ignore']});closed=new Promise(r=>child.once('close',r));
  const gatewayOrigin=await new Promise((resolve,reject)=>{let out='';const timer=setTimeout(()=>reject(Error('startup timeout')),10000);const fail=()=>{clearTimeout(timer);reject(Error('gateway failed'));};child.once('error',fail);child.once('exit',fail);child.stdout.on('data',c=>{out+=c;const m=out.match(/^(http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});});
  const config={gatewayOrigin,hostSecret,agents:[{id:'staff-1',name:'服务专员',secret:agentSecret}]};
  business=await startBusiness({accounts,serveUI});
  return {accounts,get origin(){return business.origin;},async attach(){if(support)throw Error('already attached');support=await attachSupport(business,config);},async restartBusiness(){if(!support)throw Error('attach first');const port=Number(new URL(business.origin).port);await business.close();business=null;await support.close();support=null;business=await startBusiness({accounts,serveUI,port});support=await attachSupport(business,config);},close};
 }catch(e){await close();throw e;}
}
