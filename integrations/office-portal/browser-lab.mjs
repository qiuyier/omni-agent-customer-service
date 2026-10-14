// Explicit local browser fixture. Never uses CRMEB, a database, or a model.
import {randomBytes} from 'node:crypto';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {createInterface} from 'node:readline';
import {startPortal} from './portal.mjs';
if(!process.argv.includes('--allow-test-writes'))throw Error('Requires --allow-test-writes');
const root=fileURLToPath(new URL('../../',import.meta.url)),random=()=>randomBytes(24).toString('base64url');
const dir=await mkdtemp(join(tmpdir(),'omni-office-browser-'));
let gateway,business,closed,stopping=false;
async function stop(){if(stopping)return;stopping=true;await business?.close();if(gateway&&gateway.exitCode===null&&gateway.signalCode===null){gateway.kill('SIGTERM');const timer=setTimeout(()=>gateway.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}await rm(dir,{recursive:true,force:true});}
try{
  const binary=join(dir,'gateway');await promisify(execFile)('go',['build','-o',binary,'./integrations/cross-system/gateway'],{cwd:root,timeout:120000});
  const hostSecret=random(),agentSecret=random();
  gateway=spawn(binary,[],{env:{FIXTURE_HOST_A:random(),FIXTURE_AGENT_A:random(),FIXTURE_HOST_B:hostSecret,FIXTURE_AGENT_B:agentSecret},stdio:['ignore','pipe','ignore']});
  closed=new Promise(resolve=>gateway.once('close',resolve));
  const gatewayOrigin=await new Promise((resolve,reject)=>{let output='';const timer=setTimeout(()=>reject(Error('startup timeout')),10000);gateway.once('error',()=>{clearTimeout(timer);reject(Error('gateway failed'));});gateway.once('exit',()=>{clearTimeout(timer);reject(Error('gateway stopped'));});gateway.stdout.on('data',chunk=>{output+=chunk;const m=output.match(/^(http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});});
  const accounts=[{realm:'member',handle:'lin',number:'MEM:731',passphrase:random()},{realm:'member',handle:'zhou',number:'MEM:864',passphrase:random()},{realm:'operator',handle:'staff',number:'OPS:42',passphrase:random()}];
  business=await startPortal({gatewayOrigin,hostSecret,agents:[{id:'staff-1',name:'办公服务专员',secret:agentSecret}],accounts,serveUI:true});
  console.log(JSON.stringify({origin:business.origin,testAccounts:accounts.map(({realm,handle,passphrase})=>({realm,handle,passphrase})),commands:'disable member lin | enable member lin | disable operator staff | enable operator staff | stop'}));
  const input=createInterface({input:process.stdin});
  input.on('line',line=>{const [action,role,account,...rest]=line.trim().split(/\s+/);if(action==='stop'){input.close();return;}try{if(rest.length||!['enable','disable'].includes(action))throw Error();business.setActive(role,account,action==='enable');console.log('test account state updated');}catch{console.log('invalid test command');}});
  process.once('SIGINT',()=>input.close());process.once('SIGTERM',()=>input.close());
  await new Promise(resolve=>input.once('close',resolve));
}finally{await stop();}
