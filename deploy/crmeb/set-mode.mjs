// Explicit local deployment switch. Never displays model or database credentials.
import fs from 'node:fs';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const selection=process.argv[2];if(!['ai','fake','ai-knowledge'].includes(selection))throw Error('Use set-mode.mjs ai|fake|ai-knowledge');
const mode=selection==='ai-knowledge'?'ai':selection;
const root=fileURLToPath(new URL('../../',import.meta.url));
const config='/Users/LOCAL_USER/Code/DockerProject/dnmp8/www/CRMEB/crmeb/runtime/omni-chat-deploy/config.json';
const flags=['compose','-f','deploy/crmeb/compose.yaml',...(mode==='ai'?['-f','deploy/crmeb/compose.ai.yaml']:[]),...(selection==='ai-knowledge'?['-f','deploy/crmeb/compose.knowledge.yaml']:[])];
const docker=(args)=>execFileSync('docker',args,{cwd:root,stdio:'pipe',timeout:60000});
let changed=false;
try {
 docker([...flags,'config','--quiet']);
 docker([...flags,'run','--rm','--no-deps','gateway','-check-config']);
 const current=JSON.parse(fs.readFileSync(config,'utf8'));
 fs.writeFileSync(config,JSON.stringify({...current,enabled:false},null,2)+'\n');changed=true;
 docker([...flags,'up','-d','gateway']);
 let ready=false;
 for(let n=0;n<40;n++){
  try {docker(['exec','php74','php','-r','$r=@file_get_contents("http://127.0.0.1:8093/readyz");exit($r===false?1:0);']);ready=true;break;}catch{}
  await new Promise(r=>setTimeout(r,500));
 }
 if(!ready)throw Error('not ready');
 fs.writeFileSync(config,JSON.stringify({...current,enabled:true,reply_mode:mode},null,2)+'\n');
 console.log('Local chat mode '+mode+' is ready. Model credential validity and answer quality still require acceptance.');
}catch{
 console.error(changed?'Mode switch failed; bridge remains disabled. Inspect locally and explicitly restore a known mode.':'Preflight failed; existing bridge configuration was not changed.');process.exitCode=1;
}
