import {readFileSync} from 'node:fs';
import {startServer} from './transport.mjs';
const c=JSON.parse(readFileSync('/run/transport/settings.json'));
const server=await startServer({ca:'/run/transport/ca.crt',cert:'/run/transport/server.crt',key:'/run/transport/server.key',clientFingerprint:c.clientFingerprint});
console.log('transport-ready');
for(const signal of ['SIGINT','SIGTERM'])process.once(signal,()=>server.close());
