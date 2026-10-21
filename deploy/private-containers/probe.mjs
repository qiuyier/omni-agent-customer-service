// Container-side negative probe: reports only status or TLS denial, never credentials.
import https from 'node:https';
import http from 'node:http';
import {readFileSync} from 'node:fs';
const c=JSON.parse(readFileSync('/run/business/settings.json')),mode=process.argv[2];
const options={ca:readFileSync('/run/business/ca.crt'),rejectUnauthorized:true,agent:false,headers:{'Content-Type':'application/json'},method:'POST'};
if(mode!=='no-certificate'){options.cert=readFileSync('/run/business/client.crt');options.key=readFileSync('/run/business/client.key');}
options.headers.Authorization='Bearer '+(mode==='wrong-key'?'x'.repeat(43):c.hostSecret);
const url=mode==='raw-port'?'http://gateway:8093/internal/chat/sessions':'https://gateway:8443/internal/chat/sessions';
const req=(mode==='raw-port'?http:https).request(url,options,res=>{res.resume();res.on('end',()=>console.log(JSON.stringify({status:res.statusCode})));});
req.setTimeout(3000,()=>req.destroy());req.on('error',()=>console.log(JSON.stringify({denied:true})));req.end('{"customer_id":"probe"}');
