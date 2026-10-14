// Explicit local browser fixture. Never uses CRMEB, a database, or a model.
import {randomBytes} from 'node:crypto';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {createInterface} from 'node:readline';
import {startBusiness} from './business.mjs';
if(!process.argv.includes('--allow-test-writes'))throw Error('Requires --allow-test-writes');
const root=fileURLToPath(new URL('../../',import.meta.url)),random=()=>randomBytes(24).toString('base64url');
const dir=await mkdtemp(join(tmpdir(),'omni-business-browser-'));
let gateway,business,closed,stopping=false;
async function stop(){if(stopping)return;stopping=true;await business?.close();if(gateway&&gateway.exitCode===null&&gateway.signalCode===null){gateway.kill('SIGTERM');const timer=setTimeout(()=>gateway.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}await rm(dir,{recursive:true,force:true});}
try{
  const binary=join(dir,'gateway');await promisify(execFile)('go',['build','-o',binary,'./integrations/cross-system/gateway'],{cwd:root,timeout:120000});
  const hostSecret=random(),agentSecret=random();
  gateway=spawn(binary,[],{env:{FIXTURE_HOST_A:random(),FIXTURE_AGENT_A:random(),FIXTURE_HOST_B:hostSecret,FIXTURE_AGENT_B:agentSecret},stdio:['ignore','pipe','ignore']});
  closed=new Promise(resolve=>gateway.once('close',resolve));
  const gatewayOrigin=await new Promise((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('startup timeout')),10000);gateway.once('error',()=>{clearTimeout(timer);reject(Error('gateway failed'));});gateway.once('exit',()=>{clearTimeout(timer);reject(Error('gateway stopped'));});gateway.stdout.on('data',chunk=>{output+=chunk;const m=output.match(/^(http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});});
  const accounts=[{role:'customer',account:'lin',id:'repair-member-1',password:random()},{role:'customer',account:'zhou',id:'repair-member-2',password:random()},{role:'staff',account:'staff',id:'staff-1',password:random()}];
  business=await startBusiness({gatewayOrigin,hostSecret,agents:[{id:'staff-1',name:'维修服务员工',secret:agentSecret}],accounts,serveUI:true});
  console.log(JSON.stringify({origin:business.origin,testAccounts:accounts.map(({role,account,password})=>({role,account,password})),commands:'disable customer lin | enable customer lin | disable staff staff | enable staff staff | stop'}));
  const input=createInterface({input:process.stdin});
  input.on('line',line=>{const [action,role,account,...rest]=line.trim().split(/\s+/);if(action==='stop'){input.close();return;}try{if(rest.length||!['enable','disable'].includes(action))throw Error();business.setEnabled(role,account,action==='enable');console.log('test account state updated');}catch{console.log('invalid test command');}});
  process.once('SIGINT',()=>input.close());process.once('SIGTERM',()=>input.close());
  await new Promise(resolve=>input.once('close',resolve));
}finally{await stop();}
