import {startHost} from './host.mjs';
import {mkdtemp,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes} from 'node:crypto';
import {execFile,spawn} from 'node:child_process';
import {promisify} from 'node:util';
const root=fileURLToPath(new URL('../../',import.meta.url));
export async function startSample(){
  const directory=await mkdtemp(join(tmpdir(),'omni-independent-host-'));
  let child,closed,host,stopped=false;
  const stop=async()=>{
    if(stopped)return;stopped=true;
    await host?.close();
    if(child&&child.exitCode===null&&child.signalCode===null){child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}
    await rm(directory,{recursive:true,force:true});
  };
  try{
    const binary=join(directory,'gateway');
    await promisify(execFile)('go',['build','-o',binary,'./integrations/standalone-host/gateway'],{cwd:root,timeout:120000});
    const secret=randomBytes(32).toString('base64url');
    child=spawn(binary,[],{cwd:root,env:{STANDALONE_HOST_SECRET:secret},stdio:['ignore','pipe','pipe']});
    closed=new Promise(resolve=>child.once('close',resolve));child.stderr.resume();
    const gatewayOrigin=await new Promise((resolve,reject)=>{
      const timer=setTimeout(()=>reject(Error('private gateway startup timeout')),10000);let output='';
      child.once('error',()=>{clearTimeout(timer);reject(Error('private gateway failed'));});
      child.once('exit',()=>{clearTimeout(timer);reject(Error('private gateway exited'));});
      child.stdout.on('data',chunk=>{output+=chunk;const m=output.match(/^(http:\/\/127\.0\.0\.1:\d+)\n/);if(m){clearTimeout(timer);resolve(m[1]);}});
    });
    host=await startHost({gatewayOrigin,hostSecret:secret});
    return {origin:host.origin,stop};
  }catch(e){await stop();throw e;}
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)){
  const sample=await startSample();
  console.log(`独立业务后端：${sample.origin}\n模拟登录林澈/周宁；复用现有聊天页，固定回复、内存历史。无 CRMEB、数据库或模型调用。Ctrl+C 退出。`);
  for(const signal of ['SIGINT','SIGTERM'])process.once(signal,async()=>{await sample.stop();});
}
