// Explicit local test fixture management; never accepts browser input.
import fs from 'node:fs';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../../',import.meta.url));
const name=process.argv[2],initial=process.argv[3]==='--prepare';
if(!['ecommerce-v1','ecommerce-v2','withdraw'].includes(name))throw Error('Choose ecommerce-v1, ecommerce-v2, or explicit withdraw');
const dir=root+'knowledge.local/crmeb-webchat',target=dir+'/faq.json';
if(initial&&fs.existsSync(target))throw Error('Already prepared; use explicit reload');
if(!initial&&!fs.existsSync(target))throw Error('Prepare before reloading');
const data=fs.readFileSync(root+'integrations/crmeb/knowledge-fixtures/'+name+'.json');
JSON.parse(data);const version=createHash('sha256').update(data).digest('hex');
fs.mkdirSync(dir,{recursive:true,mode:0o755});
fs.writeFileSync(dir+'/faq.next',data,{mode:0o644});fs.renameSync(dir+'/faq.next',target);
if(initial){console.log(JSON.stringify({prepared:true,fixture:name,version}));process.exit(0);}
const container='omni-crmeb-webchat-test-gateway-1';
const docker=args=>execFileSync('docker',args,{stdio:['ignore','pipe','pipe'],timeout:10000}).toString();
const since=new Date().toISOString();
try{
 docker(['kill','--signal=HUP',container]);
 let found;
 for(let n=0;n<40;n++){
  // Docker JSON state messages are written to stderr, so read both streams.
  const result=await import('node:child_process').then(({spawnSync})=>spawnSync('docker',['logs','--since',since,container],{encoding:'utf8',timeout:10000}));
  for(const line of ((result.stdout||'')+'\n'+(result.stderr||'')).split('\n')){try{const e=JSON.parse(line);if(e.event==='knowledge_reload_succeeded'&&e.knowledge.version===version)found=e.knowledge;}catch{}}
  if(found)break;await new Promise(r=>setTimeout(r,250));
 }
 if(!found)throw Error('unconfirmed');
 console.log(JSON.stringify({reloaded:true,fixture:name,version,entries:found.entries}));
}catch{console.error('Reload not confirmed; do not assume the new file is active. Inspect knowledge state or restore the previous fixture.');process.exitCode=1;}
