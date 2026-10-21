import http from 'node:http';
import {readFileSync} from 'node:fs';
import {startClient} from './transport.mjs';
import {startBusiness} from '../../integrations/fresh-onboarding/business.mjs';
import {attachSupport} from '../../integrations/fresh-onboarding/support.mjs';
const settings='/run/business/settings.json';
let c;
for(let i=0;i<300;i++){c=JSON.parse(readFileSync(settings));if(c.origin)break;await new Promise(r=>setTimeout(r,100));}
const publicURL=new URL(c.origin);
if(publicURL.hostname!=='127.0.0.1'||publicURL.protocol!=='http:'||publicURL.origin!==c.origin)throw Error('local_test_origin_required');
const client=await startClient({origin:'https://gateway:8443',ca:'/run/business/ca.crt',cert:'/run/business/client.crt',key:'/run/business/client.key'});
const business=await startBusiness({accounts:c.accounts});
const support=await attachSupport(business,{gatewayOrigin:'http://127.0.0.1:8094',hostSecret:c.hostSecret,agents:c.agents,storage:'postgres'});
const paths=new Set(['/auth/customer/login','/auth/customer/logout','/auth/customer/me','/auth/staff/login','/auth/staff/logout','/auth/staff/me','/host/me','/host/chat-session','/chat/v1/messages','/chat/v1/message-receipt','/chat/v1/handoff','/chat/v1/consultations','/chat/v1/previous-consultation','/desk/me','/desk/queue','/desk/action']);
const edge=http.createServer(async(req,res)=>{
 const fail=status=>res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'}).end('{"error":"unavailable"}');
 try{
  const count=name=>req.rawHeaders.filter((v,i)=>i%2===0&&v.toLowerCase()===name).length;
  if(req.headers.host!==publicURL.host||count('host')!==1||count('origin')>1||req.headers['sec-fetch-site']==='cross-site'||(req.headers.origin&&req.headers.origin!==c.origin)||(req.method==='POST'&&req.headers.origin!==c.origin))return fail(403);
  if(!paths.has(req.url))return fail(404);
  if(!['GET','POST'].includes(req.method))return fail(405);
  const headers={Origin:business.origin};
  for(const name of ['cookie','content-type','authorization','x-business-session']){if(count(name)>1)return fail(400);if(req.headers[name])headers[name]=req.headers[name];}
  const chunks=[];let size=0;for await(const chunk of req){size+=chunk.length;if(size>32768)return fail(413);chunks.push(chunk);}
  if(req.method==='GET'&&size)return fail(400);
  const r=await fetch(business.origin+req.url,{method:req.method,headers,body:req.method==='POST'?Buffer.concat(chunks):undefined,redirect:'error',signal:AbortSignal.timeout(13000)});
  const data=Buffer.from(await r.arrayBuffer());if(data.length>1048576)return fail(502);
  const out={'Content-Type':'application/json','Cache-Control':'no-store'};const cookie=r.headers.getSetCookie();if(cookie.length)out['Set-Cookie']=cookie;
  res.writeHead(r.status,out).end(data);
 }catch{if(!res.headersSent)fail(503);else res.destroy();}
});
edge.headersTimeout=5000;edge.requestTimeout=15000;edge.maxHeadersCount=32;
await new Promise(r=>edge.listen(8080,'0.0.0.0',r));console.log('business-ready');
for(const signal of ['SIGINT','SIGTERM'])process.once(signal,async()=>{edge.closeAllConnections();await new Promise(r=>edge.close(r));await business.close();await support.close();await client.close();});
