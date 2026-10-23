// Backend process wrapper for the local restart harness; never reads .env.
import {startHost} from './host.mjs';
let offset=0;
const host=await startHost({now:()=>Date.now()+offset,gatewayOrigin:process.env.STANDALONE_GATEWAY_ORIGIN,hostSecret:process.env.STANDALONE_HOST_SECRET,storage:'postgres',replyMode:process.env.STANDALONE_REPLY_MODE||'fake',agents:JSON.parse(process.env.STANDALONE_AGENTS_JSON||'[]'),port:Number(process.env.STANDALONE_PUBLIC_PORT||0)});
// Private parent IPC only; no public fault-injection endpoint. Exercise normal
// expiry cleanup, then restore the clock so new gateway tokens remain valid.
process.on('message',async message=>{
 if(message?.type!=='expire-demo-login')return;
 let ok=false;
 try{offset=1800001;const response=await fetch(host.origin+'/host/me',{signal:AbortSignal.timeout(3000)});await response.body?.cancel();ok=response.ok;}
 catch{ok=false;}
 finally{offset=0;process.send?.({type:'expire-demo-login-done',ok});}
});
console.log(host.origin);
for(const signal of ['SIGINT','SIGTERM'])process.once(signal,async()=>{await host.close();if(process.connected)process.disconnect();});
