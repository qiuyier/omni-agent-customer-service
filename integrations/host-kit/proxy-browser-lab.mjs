// Local regression controls only; no HTTP administration endpoints.
import {createInterface} from 'node:readline';
import {createFixture} from '../fresh-onboarding/fixture.mjs';
import {startResponseGate} from '../office-portal/response-gate.mjs';
if(process.argv.length!==4||process.argv[2]!=='--allow-test-writes'||!['fresh','office'].includes(process.argv[3]))throw Error('requires --allow-test-writes fresh|office');
const fixture=await createFixture({serveUI:true,system:process.argv[3]});let gate;
try{
 await fixture.attach();gate=await startResponseGate(fixture.origin,{notify:event=>console.log(event)});
 console.log(JSON.stringify({system:process.argv[3],origin:gate.origin,accounts:fixture.accounts.map(({handle,passphrase})=>({account:handle,password:passphrase})),commands:'hold | release | status | restart | stop'}));
 const input=createInterface({input:process.stdin});for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>input.close());
 for await(const line of input){const command=line.trim();if(command==='stop')break;try{if(command==='hold')gate.arm();else if(command==='release')gate.release();else if(command==='status')console.log(JSON.stringify(gate.status()));else if(command==='restart'){await fixture.restartBusiness();console.log('business and adapter recreated; gateway unchanged');}else console.log('invalid command');}catch{console.log('test control failed');}}
}finally{process.stdin.pause();await gate?.close();await fixture.close();console.log('fixture stopped');}
