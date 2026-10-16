// Local business facade: all office-specific field and route differences stay here.
import http from 'node:http';
import {createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {createLegacyDirectory} from './legacy.mjs';
import {startAdapter} from '../host-kit/adapter.mjs';
import {businessIdentity} from '../host-kit/identity.example.mjs';
import {createSupportProxy} from '../host-kit/proxy.mjs';

const fail=(status,code)=>Object.assign(Error(code),{status,code});
const roleToRealm={customer:'member',staff:'operator'};
const realmToRole={member:'customer',operator:'staff'};
// Native numbers contain ':' and cannot be passed straight to the common identity API.
const subject=record=>record.principal.realm==='operator'&&record.principal.number==='OPS:42'?'staff-1':(record.principal.realm==='member'?'member_':'operator_')+createHash('sha256').update(record.principal.number).digest('hex');
const pages=new Set(['/access/member','/access/operator','/support/chat','/support/desk']);
const assets=new Map([['/portal.js','app.js'],['/portal.css','app.css']]);
async function bounded(stream,limit){let size=0;const parts=[];if(stream)for await(const part of stream){size+=part.length;if(size>limit)throw fail(413,'payload_limit');parts.push(Buffer.from(part));}return Buffer.concat(parts);}
function object(body,keys){if(!body||Array.isArray(body)||typeof body!=='object'||Object.keys(body).some(key=>!keys.includes(key)))throw fail(400,'invalid_request');return body;}
export async function startPortal({gatewayOrigin,hostSecret,agents=[],accounts,serveUI=false,now=Date.now,storage='memory',port=0}){
 const directory=await createLegacyDirectory({accounts,now});
 const sessions={authenticate(request,role){
  const realm=roleToRealm[role];if(!realm)return null;
  const record=directory.authenticate(request,realm);
  if(!record||record.principal.realm!==realm||record.lifecycle.active!==true||!Number.isFinite(record.ticket.deadline)||record.ticket.deadline<=now())return null;
  return {role,enabled:true,subjectID:subject(record),displayName:record.principal.number,sessionID:record.ticket.key,expiresAt:record.ticket.deadline,version:record.ticket.publicRevision};
 }};
 let adapter;
 try{adapter=await startAdapter({gatewayOrigin,hostSecret,agents,now,storage,identity:businessIdentity({sessions})});}catch(error){directory.dispose();throw error;}
 let origin,active=0,closed=false;
 const proxy=createSupportProxy({adapterOrigin:adapter.origin,publicOrigin:()=>origin,sessions,now});
 const respond=(response,status,data)=>{response.writeHead(status,{'Content-Type':'application/json; charset=utf-8'});response.end(data===undefined?'':JSON.stringify(data));};
 const server=http.createServer(async(request,response)=>{
  response.setHeader('Cache-Control','no-store');response.setHeader('X-Content-Type-Options','nosniff');response.setHeader('Referrer-Policy','no-referrer');
  response.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'");
  if(active>=16){respond(response,429,{code:'busy'});return;}active++;
  try{
   const count=name=>request.rawHeaders.filter((_,index)=>index%2===0&&request.rawHeaders[index].toLowerCase()===name).length;
   if(count('host')!==1||request.headers.host!==new URL(origin).host||count('origin')>1||request.headers['sec-fetch-site']==='cross-site'||(request.headers.origin&&request.headers.origin!==origin))throw fail(403,'request_not_allowed');
   const path=request.url;
   if(!path.startsWith('/')||/[?%#]/.test(path))throw fail(400,'invalid_path');
   if(request.method==='POST'&&request.headers.origin!==origin)throw fail(403,'origin_required');
   if(request.method==='POST'&&(count('content-type')!==1||request.headers['content-type'].split(';')[0].trim()!=='application/json'))throw fail(415,'json_required');
   if(serveUI&&request.method==='GET'){
    if(path==='/'){response.writeHead(302,{Location:'/access/member'});response.end();return;}
    if(pages.has(path)||assets.has(path)){
     const asset=assets.get(path),data=await readFile(new URL('./public/'+(asset||'index.html'),import.meta.url));
     response.writeHead(200,{'Content-Type':asset==='app.js'?'text/javascript':asset==='app.css'?'text/css':'text/html; charset=utf-8'});response.end(data);return;
    }
   }
   const identity=/^\/support\/identity\/(member|operator)$/.exec(path);
   if(identity){
    if(request.method!=='GET')throw fail(405,'method_not_allowed');
    const record=directory.authenticate(request,identity[1]);
    respond(response,200,{identity:record?{id:subject(record),role:realmToRole[identity[1]],version:record.ticket.publicRevision}:null});return;
   }
   const access=/^\/access\/(member|operator)\/(open|close)$/.exec(path);
   if(access){
    if(request.method!=='POST')throw fail(405,'method_not_allowed');
    let body;try{body=JSON.parse((await bounded(request,8192)).toString());}catch(error){if(error.status)throw error;throw fail(400,'invalid_request');}
    const [,realm,operation]=access;
    if(operation==='close'){object(body,[]);response.setHeader('Set-Cookie',directory.close(request,realm));respond(response,204);return;}
    object(body,['handle','passphrase']);
    let cookie;try{cookie=await directory.open(request,realm,body.handle,body.passphrase);}catch(error){if(error.limit)throw fail(429,'login_limit');throw error;}
    if(!cookie)throw fail(401,'login_required');
    response.setHeader('Set-Cookie',cookie);respond(response,200,{opened:true});return;
   }
   if(!await proxy(request,response))throw fail(404,'not_found');
  }catch(error){if(!response.headersSent)respond(response,error.status||500,{code:error.code||'internal_error'});else response.end();}
  finally{active--;}
 });
 server.requestTimeout=25000;server.headersTimeout=5000;server.keepAliveTimeout=5000;server.maxHeadersCount=64;
 try{await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(port,'127.0.0.1',resolve);});}catch(error){directory.dispose();await adapter.close();throw error;}
 origin=`http://127.0.0.1:${server.address().port}`;
 return {origin,setActive:(realm,handle,value)=>directory.setActive(realm,handle,value),close:async()=>{if(closed)return;closed=true;directory.dispose();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));await adapter.close();}};
}
