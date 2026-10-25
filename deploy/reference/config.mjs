import {randomBytes} from 'node:crypto';
import {mkdir,writeFile,readFile,lstat} from 'node:fs/promises';
import {join} from 'node:path';
export const ports={database:15435,gateway:8095,business:8096};
const secret=()=>randomBytes(32).toString('hex');
export function newConfig(){
 return {version:1,hostID:'reference-local',hostSecret:secret(),databasePassword:secret(),agentSecret:secret(),accounts:[
  {realm:'member',handle:'customer_a',number:'MEM:reference-a',passphrase:secret()},
  {realm:'member',handle:'customer_b',number:'MEM:reference-b',passphrase:secret()},
  {realm:'operator',handle:'staff',number:'OPS:42',passphrase:secret()},
 ]};
}
export function validate(c){
 if(!c||c.version!==1||c.hostID!=='reference-local'||Object.keys(c).sort().join(',')!=='accounts,agentSecret,databasePassword,hostID,hostSecret,version')throw Error('invalid reference configuration');
 for(const k of ['hostSecret','databasePassword','agentSecret'])if(!/^[a-f0-9]{64}$/.test(c[k]))throw Error('invalid reference credential');
 if(new Set([c.hostSecret,c.databasePassword,c.agentSecret]).size!==3)throw Error('credentials must be independent');
 const expected=[['member','customer_a','MEM:reference-a'],['member','customer_b','MEM:reference-b'],['operator','staff','OPS:42']];
 if(!Array.isArray(c.accounts)||c.accounts.length!==3)throw Error('invalid reference accounts');
 for(let i=0;i<3;i++){
  const a=c.accounts[i];if(!a||Object.keys(a).sort().join(',')!=='handle,number,passphrase,realm'||[a.realm,a.handle,a.number].join('|')!==expected[i].join('|')||!/^[a-f0-9]{64}$/.test(a.passphrase))throw Error('invalid reference account');
 }
 return c;
}
export function environment(c){
 validate(c);
 return {PATH:process.env.PATH,DATABASE_URL:`postgres://omni_reference:${c.databasePassword}@127.0.0.1:${ports.database}/omni_reference?sslmode=disable`,WEBCHAT_ADDRESS:`127.0.0.1:${ports.gateway}`,WEBCHAT_HOST_ID:c.hostID,WEBCHAT_HOST_SECRET:c.hostSecret,WEBCHAT_REPLY_MODE:'fake',WEBCHAT_AGENTS_JSON:JSON.stringify([{source_id:c.hostID,id:'staff-1',secret:c.agentSecret}])};
}
export async function initialize(directory){
 // Exclusive directory creation prevents regenerating identities/passwords over
 // a live volume. Partial initialization is left for explicit operator review.
 await mkdir(directory,{mode:0o700});const c=newConfig();
 await writeFile(join(directory,'config.json'),JSON.stringify(c,null,2)+'\n',{flag:'wx',mode:0o600});
 await writeFile(join(directory,'postgres-password'),c.databasePassword,{flag:'wx',mode:0o600});
 return c;
}
export async function load(directory){
 const file=join(directory,'config.json'),stat=await lstat(file);
 if(!stat.isFile()||stat.isSymbolicLink()||(stat.mode&0o077)||stat.size>16384)throw Error('unsafe reference configuration file');
 const c=validate(JSON.parse(await readFile(file,'utf8')));
 if(await readFile(join(directory,'postgres-password'),'utf8')!==c.databasePassword)throw Error('database credential files differ');
 return c;
}
