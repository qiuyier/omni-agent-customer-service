// Test-only launcher: fixed dedicated local database, no ambient credentials.
import {randomBytes} from 'node:crypto';
import {mkdtemp} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {createInterface} from 'node:readline';
import {startPortal} from '../office-portal/portal.mjs';
import {startBusiness} from '../fresh-onboarding/business.mjs';
import {attachSupport} from '../fresh-onboarding/support.mjs';
if(process.argv.length!==3||process.argv[2]!=='--allow-test-writes')throw Error('requires --allow-test-writes');
const root=fileURLToPath(new URL('../../',import.meta.url)),dir=await mkdtemp(join(tmpdir(),'omni-pg-browser-'));
const database='postgres://webchat_demo:webchat_demo_local@127.0.0.1:15433/omni_agent_webchat_demo?sslmode=disable';
const random=()=>randomBytes(24).toString('hex'),run=random(),hosts=[0,1].map(i=>({source_id:`browser-${run}-${i}`,secret:random()})),agents=hosts.map(h=>({source_id:h.source_id,id:'staff-1',secret:random()}));
const credentials=[0,1].map(()=>[{realm:'member',handle:'lin',number:'MEM:731',passphrase:random()},{realm:'member',handle:'zhou',number:'MEM:864',passphrase:random()},{realm:'operator',handle:'staff',number:'OPS:42',passphrase:random()}]);
let child,exited,gatewayOrigin,office,fresh,support,input;
const stop=async()=>{if(child&&child.exitCode===null&&child.signalCode===null){child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),7000);try{await exited;}finally{clearTimeout(timer);}}};
const start=async()=>{
 const port=gatewayOrigin?new URL(gatewayOrigin).port:'0';
 child=spawn(join(dir,'gateway'),[],{env:{PATH:process.env.PATH,LANG:'C',WEBCHAT_ADDRESS:`127.0.0.1:${port}`,DATABASE_URL:database,STORAGE_DRIVER:'postgres',WEBCHAT_REPLY_MODE:'fake',WEBCHAT_HOSTS_JSON:JSON.stringify(hosts),WEBCHAT_AGENTS_JSON:JSON.stringify(agents)},stdio:['ignore','pipe','ignore']});
 exited=new Promise(resolve=>child.once('close',resolve));
 gatewayOrigin=await new Promise((resolve,reject)=>{let out='';const timer=setTimeout(()=>reject(Error('gateway startup timeout')),15000);const fail=()=>{clearTimeout(timer);reject(Error('gateway startup failed'));};child.once('error',fail);child.once('exit',fail);child.stdout.on('data',chunk=>{out+=chunk;const m=out.match(/^Web chat server: (http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});});
 console.log(JSON.stringify({gatewayPID:child.pid,event:'gateway ready',storage:'postgres',mode:'fake'}));
};
try{
 for(const [name,pkg] of [['gateway','webchat-server'],['migrate','migrate']])await promisify(execFile)('go',['build','-o',join(dir,name),`./cmd/${pkg}`],{cwd:root,timeout:120000});
 await promisify(execFile)(join(dir,'migrate'),[],{env:{PATH:process.env.PATH,DATABASE_URL:database},timeout:30000});
 await start();
 const config=i=>({gatewayOrigin,hostSecret:hosts[i].secret,agents:[{id:'staff-1',name:'服务专员',secret:agents[i].secret}],storage:'postgres'});
 office=await startPortal({...config(0),accounts:credentials[0],serveUI:true});
 fresh=await startBusiness({accounts:credentials[1],serveUI:true});support=await attachSupport(fresh,config(1));
 console.log(JSON.stringify({office:office.origin,fresh:fresh.origin,accounts:credentials.map(list=>list.map(({handle,passphrase})=>({account:handle,password:passphrase}))),commands:'restart | stop'}));
 input=createInterface({input:process.stdin});for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>input.close());
 for await(const line of input){if(line.trim()==='stop')break;if(line.trim()==='restart'){await stop();await start();}else console.log('invalid command');}
}catch{console.error('postgres browser lab failed');process.exitCode=1;}
finally{input?.close();process.stdin.pause();await office?.close();await fresh?.close();await support?.close();await stop();console.log('lab stopped; database retained');}
