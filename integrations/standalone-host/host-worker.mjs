// Backend process wrapper for the local restart harness; never reads .env.
import {startHost} from './host.mjs';
const host=await startHost({gatewayOrigin:process.env.STANDALONE_GATEWAY_ORIGIN,hostSecret:process.env.STANDALONE_HOST_SECRET,storage:'postgres',replyMode:process.env.STANDALONE_REPLY_MODE||'fake',port:Number(process.env.STANDALONE_PUBLIC_PORT||0)});
console.log(host.origin);
for(const signal of ['SIGINT','SIGTERM'])process.once(signal,async()=>{await host.close();});
