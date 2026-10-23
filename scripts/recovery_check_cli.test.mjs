import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
async function cli(args,onOutput=()=>{}){
 const child=spawn(process.execPath,['scripts/recovery_check.mjs',...args],{cwd:root,stdio:['ignore','pipe','pipe']});let output='';
 for(const stream of [child.stdout,child.stderr])stream.on('data',b=>{output+=b;onOutput(child,output);});
 const code=await new Promise((resolve,reject)=>{child.once('error',reject);child.once('close',resolve);});return {code,output};
}
test('CLI refuses database writes without opt-in before creating a report',async()=>{
 const r=await cli(['--postgres']);assert.equal(r.code,2);assert.doesNotMatch(r.output,/Report directory/);
});
test('CLI help does not start tests',async()=>{
 const r=await cli(['--help']);assert.equal(r.code,0);assert.doesNotMatch(r.output,/RUN web/);
});
test('SIGINT produces a non-green report and exits',{timeout:20000},async()=>{
 let sent=false;const r=await cli([], (child,text)=>{if(!sent&&text.includes('RUN web-and-host')){sent=true;child.kill('SIGINT');}});
 assert.equal(r.code,1);assert.equal(sent,true);
 const directory=r.output.match(/Report directory: (.+)/)?.[1];assert.ok(directory);
 const report=JSON.parse(await readFile(directory+'/report.json','utf8'));
 assert.equal(report.status,'interrupted');assert.equal(report.postgres,'not_run');assert.ok(report.manual_required.length);
});
