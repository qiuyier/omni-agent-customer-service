import http from 'node:http';
import https from 'node:https';
import {readFileSync} from 'node:fs';

const routes = new Map([
 ['/internal/chat/sessions',['POST']], ['/internal/chat/desk/queue',['GET']], ['/internal/chat/desk/action',['POST']],
 ['/chat/v1/messages',['GET','POST']], ['/chat/v1/message-receipt',['POST']], ['/chat/v1/handoff',['POST']],
 ['/chat/v1/consultations',['POST']], ['/chat/v1/previous-consultation',['GET']],
]);
export const permitted = (method,path) => routes.get(path)?.includes(method) === true;
const error = (res,status) => {if(!res.headersSent)res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'}).end('{"error":"transport_unavailable"}');else res.destroy();};
async function bounded(stream,max) {const chunks=[];let n=0;for await(const chunk of stream){n+=chunk.length;if(n>max)throw Error('limit');chunks.push(Buffer.from(chunk));}return Buffer.concat(chunks);}

// Fixed target, exact methods/paths, stripped headers, no redirect following.
function relay(target,tls={},authorize=()=>true) {
 return async(req,res)=>{
  if(!authorize(req))return error(res,403);
  if(!permitted(req.method,req.url))return error(res,404);
  const count=name=>req.rawHeaders.filter((v,i)=>i%2===0&&v.toLowerCase()===name).length;
  if(count('authorization')!==1||!/^Bearer [A-Za-z0-9_.-]{20,4096}$/.test(req.headers.authorization||''))return error(res,401);
  if(req.method==='POST'&&(count('content-type')!==1||req.headers['content-type'].split(';')[0].trim()!=='application/json'))return error(res,415);
  try {
   const body=await bounded(req,32768);
   if(req.method==='GET'&&body.length)return error(res,400);
   const headers={Authorization:req.headers.authorization,'Content-Type':'application/json','Content-Length':body.length};
   const result=await new Promise((resolve,reject)=>{
    const outgoing=(target.protocol==='https:'?https:http).request(new URL(req.url,target),{...tls,method:req.method,headers,agent:false},async response=>{
     try{const data=await bounded(response,1048576);if(!response.headers['content-type']?.startsWith('application/json'))throw Error('type');resolve({status:response.statusCode,data});}catch{outgoing.destroy();reject(Error('response'));}
    });
    const timer=setTimeout(()=>outgoing.destroy(Error('timeout')),10000);
    outgoing.once('close',()=>clearTimeout(timer));outgoing.once('error',reject);outgoing.end(body);
   });
   res.writeHead(result.status,{'Content-Type':'application/json','Cache-Control':'no-store'}).end(result.data);
  }catch{error(res,503);}
 };
}
async function listen(server,host,port){server.headersTimeout=5000;server.requestTimeout=15000;server.maxHeadersCount=32;await new Promise((r,j)=>{server.once('error',j);server.listen(port,host,r);});return {port:server.address().port,close:async()=>{server.closeAllConnections();await new Promise(r=>server.close(r));}};}

export function startClient({origin,ca,cert,key,port=8094}) {
 const u=new URL(origin);
 if(u.protocol!=='https:'||u.origin!==origin||u.hostname!=='gateway'||u.port!=='8443')throw Error('fixed_private_target_required');
 return listen(http.createServer(relay(u,{ca:readFileSync(ca),cert:readFileSync(cert),key:readFileSync(key),rejectUnauthorized:true,minVersion:'TLSv1.2'})),'127.0.0.1',port);
}
export function startServer({ca,cert,key,clientFingerprint,port=8443,upstreamPort=8093}) {
 if(!/^([A-F0-9]{2}:){31}[A-F0-9]{2}$/.test(clientFingerprint)||!Number.isInteger(upstreamPort)||upstreamPort<1||upstreamPort>65535)throw Error('invalid_peer_configuration');
 const server=https.createServer({ca:readFileSync(ca),cert:readFileSync(cert),key:readFileSync(key),requestCert:true,rejectUnauthorized:true,minVersion:'TLSv1.2'},relay(new URL('http://127.0.0.1:'+upstreamPort),{},req=>req.socket.authorized&&req.socket.getPeerCertificate().fingerprint256===clientFingerprint));
 server.on('tlsClientError',()=>{});
 return listen(server,'0.0.0.0',port);
}
