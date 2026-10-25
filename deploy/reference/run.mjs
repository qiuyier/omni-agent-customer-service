// Stable local reference entry point. No random runtime identity, fault proxy,
// model configuration, migrations-on-start or automatic Docker lifecycle.
import {mkdir} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {spawn} from 'node:child_process';
import {initialize,load,environment,ports} from './config.mjs';
import {assertAvailable} from './ports.mjs';
import {startBusiness} from '../../integrations/fresh-onboarding/business.mjs';
import {attachSupport} from '../../integrations/fresh-onboarding/support.mjs';
const root=fileURLToPath(new URL('../../',import.meta.url));
const directory=root+'.local/reference';
let child,business,support;
let stopping=false;
const run=(name,args,env)=>{
 const p=spawn(root+'bin/reference/'+name,args,{cwd:root,env,stdio:'inherit'});
 p.done=new Promise(resolve=>{p.once('error',()=>resolve(1));p.once('exit',code=>resolve(code??1));});
 return p;
};
async function shutdown(){
 if(support)await support.close();
 if(business)await business.close();
 if(child&&child.exitCode===null&&child.signalCode===null){child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),8000);await child.done;clearTimeout(timer);}
}
try{
 const command=process.argv[2];if(process.argv.length!==3||!['init','check','migrate','start'].includes(command))throw Error('invalid command');
 if(command==='init'){
  await mkdir(root+'.local',{recursive:true,mode:0o700});await initialize(directory);
  console.log('Initialized .local/reference once. Private test passwords are in config.json; do not publish this file.');
 }else{
  const c=await load(directory),env=environment(c);
  if(command!=='start'){
   for(const [name,args] of command==='migrate'?[['migrate',[]]]:[['webchat-server',['-check-config']],['webchat-server',['-check-resources']]]){
    const code=await run(name,args,env).done;if(code!==0)throw Error('reference command failed');
   }
  }else{
   // Refuse a second instance before spawning; never use an existing unrelated
   // gateway's readyz as proof our child started. Child exit is also observed.
   await assertAvailable(ports.gateway);await assertAvailable(ports.business);
   const abort=new AbortController();
   for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>{stopping=true;abort.abort();});
   child=run('webchat-server',[],env);
   child.done.then(()=>{abort.abort();});
   let ready=false;
   for(let n=0;n<50&&!abort.signal.aborted;n++){
    try{const r=await fetch(`http://127.0.0.1:${ports.gateway}/readyz`,{signal:AbortSignal.timeout(500)});await r.body?.cancel();if(r.status===200){ready=true;break;}}catch{}
    await new Promise(resolve=>setTimeout(resolve,100));
   }
   if(!ready||abort.signal.aborted)throw Error('gateway not ready');
   business=await startBusiness({accounts:c.accounts,port:ports.business,serveUI:true,cookieNamespace:'reference'});
   support=await attachSupport(business,{gatewayOrigin:`http://127.0.0.1:${ports.gateway}`,hostSecret:c.hostSecret,agents:[{id:'staff-1',name:'演示坐席',secret:c.agentSecret}],storage:'postgres'});
   console.log(`LOCAL REFERENCE ONLY: ${business.origin}/ (customer), ${business.origin}/staff-login (staff). Demo authentication; fake replies; PostgreSQL retained.`);
   if(!abort.signal.aborted)await new Promise(resolve=>abort.signal.addEventListener('abort',resolve,{once:true}));
   if(!stopping)throw Error('gateway exited unexpectedly');
  }
 }
}catch{console.error('Reference command failed. Check command, private configuration, built binaries, database readiness and occupied ports. No credentials displayed.');process.exitCode=1;}
finally{await shutdown();}
