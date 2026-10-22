import {readFile,writeFile,copyFile,rename} from 'node:fs/promises';
import {join} from 'node:path';
import {randomBytes,X509Certificate} from 'node:crypto';
// Only receives this run's owned names/paths. Never touches external deployments.
export function rotationLab({dir,config,docker,ssl,gateway,transport,business,image,network,nodeMount,ensure,wait}){
 let state='original',nextSecret;
 const atomic=async(path,data)=>{await writeFile(path+'.next',data,{mode:0o600});await rename(path+'.next',path);};
 const probe=async mode=>JSON.parse((await docker('exec',business,'node','/src/deploy/private-containers/probe.mjs',mode)).stdout);
 async function stage(){
  if(state!=='original')throw Error('rotation_already_staged');
  nextSecret=randomBytes(32).toString('base64url');
  await ssl('req','-newkey','rsa:2048','-nodes','-keyout','next-client.key','-out','next-client.csr','-subj','/CN=rotated-client');
  await ssl('x509','-req','-in','next-client.csr','-CA','ca/cert','-CAkey','ca/key','-CAcreateserial','-out','next-client.crt','-days','1','-extfile','client.ext');
  for(const ext of ['crt','key'])await copyFile(join(dir,'business/client.'+ext),join(dir,'business/retired-client.'+ext));
  await atomic(join(dir,'business/retired.json'),JSON.stringify({hostSecret:config.hostSecret}));
  // Prepare all material before closing the old trust boundary.
  const env=await readFile(join(dir,'gateway.env'),'utf8');
  await atomic(join(dir,'gateway.env'),env.replace(/^WEBCHAT_HOST_SECRET=.*$/m,'WEBCHAT_HOST_SECRET='+nextSecret));
  const fingerprint=new X509Certificate(await readFile(join(dir,'next-client.crt'))).fingerprint256;
  await docker('rm','-f',transport);await docker('rm','-f',gateway);
  await docker('run','-d','--name',gateway,'--network',network,'--network-alias','gateway','--env-file',join(dir,'gateway.env'),image);
  await atomic(join(dir,'transport/settings.json'),JSON.stringify({clientFingerprint:fingerprint}));
  await docker('run','-d','--name',transport,'--network','container:'+gateway,...nodeMount,'-v',join(dir,'transport')+':/run/transport:ro','node:24-alpine','node','/src/deploy/private-containers/gateway-worker.mjs');
  await wait(async()=>(await probe('retired-certificate')).status===403);
  ensure((await probe('retired-certificate')).status===403,'rotation_old_certificate_revoked');
  state='staged';
 }
 async function activate(){
  if(state!=='staged')throw Error('rotation_not_staged');
  for(const ext of ['crt','key'])await atomic(join(dir,'business/client.'+ext),await readFile(join(dir,'next-client.'+ext)));
  config.hostSecret=nextSecret;await atomic(join(dir,'business/settings.json'),JSON.stringify(config));
  const before=((await docker('logs',business)).stdout.match(/support-reloaded/g)||[]).length;
  await docker('kill','--signal','SIGUSR2',business);
  await wait(async()=>((await docker('logs',business)).stdout.match(/support-reloaded/g)||[]).length>before);
  state='active';
 }
 async function verify(){
  if(state!=='active')throw Error('rotation_not_active');
  ensure((await probe('current')).status===201,'rotation_new_credentials_accepted');
  ensure((await probe('retired-key')).status===401,'rotation_old_key_revoked');
  ensure((await probe('retired-certificate')).status===403,'rotation_retired_cert_with_new_key_denied');
 }
 async function rejectInvalid(){
  if(state!=='staged')throw Error('rotation_not_staged');
  await atomic(join(dir,'business/settings.json'),JSON.stringify({...config,hostSecret:'invalid'}));
  const before=((await docker('logs',business)).stdout.match(/support-reload-failed/g)||[]).length;
  await docker('kill','--signal','SIGUSR2',business);
  await wait(async()=>((await docker('logs',business)).stdout.match(/support-reload-failed/g)||[]).length>before);
  ensure((await probe('retired-certificate')).status===403,'rotation_invalid_reload_does_not_restore_retired_trust');
 }
 return {stage,activate,verify,rejectInvalid};
}
