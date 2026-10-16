import {createInterface} from 'node:readline';
import {createFixture} from './fixture.mjs';
if(process.argv.length!==3||process.argv[2]!=='--allow-test-writes')throw Error('requires --allow-test-writes');
const fixture=await createFixture({serveUI:true});
try{
 console.log(JSON.stringify({origin:fixture.origin,accounts:fixture.accounts.map(({realm,handle,passphrase})=>({role:realm==='member'?'customer':'staff',account:handle,password:passphrase})),commands:'attach | restart-business | stop',baseline:'authentication only; support routes absent'}));
 const input=createInterface({input:process.stdin});for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>input.close());
 for await(const line of input){if(line.trim()==='stop')break;if(line.trim()==='attach'){await fixture.attach();console.log('support attached');}else if(line.trim()==='restart-business'){await fixture.restartBusiness();console.log('business and adapter recreated; gateway unchanged');}else console.log('invalid command');}
}finally{process.stdin.pause();await fixture.close();console.log('fixture stopped');}
