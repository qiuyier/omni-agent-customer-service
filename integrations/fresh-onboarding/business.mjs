// Baseline business: password login and identity only. No support dependency.
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {createLegacyDirectory} from '../office-portal/legacy.mjs';
const realm=role=>role==='staff'?'operator':'member';
export const fail=(status,code)=>Object.assign(Error(code),{status,code});
export async function bounded(stream,limit){let size=0;const parts=[];for await(const part of stream){size+=part.length;if(size>limit)throw fail(413,'payload_limit');parts.push(Buffer.from(part));}return Buffer.concat(parts);}
export async function startBusiness({accounts,port=0,serveUI=false,cookieNamespace='fresh'}){
 const directory=await createLegacyDirectory({accounts,cookieNamespace});let extension=null,origin,active=0;
 const identity=(request,role)=>{
  const record=directory.authenticate(request,realm(role));if(!record)return null;
  return {subjectID:record.principal.realm==='operator'&&record.principal.number==='OPS:42'?'staff-1':'person_'+createHash('sha256').update(record.principal.number).digest('hex'),displayName:record.principal.number,role,enabled:record.lifecycle.active,sessionID:record.ticket.key,expiresAt:record.ticket.deadline,version:record.ticket.publicRevision};
 };
 const send=(res,status,data)=>{res.writeHead(status,{'Content-Type':'application/json; charset=utf-8'});res.end(data===undefined?'':JSON.stringify(data));};
 const server=http.createServer(async(req,res)=>{
  res.setHeader('Cache-Control','no-store');res.setHeader('X-Content-Type-Options','nosniff');res.setHeader('Referrer-Policy','no-referrer');
  res.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'");
  if(active>=16){send(res,429,{code:'busy'});return;}active++;
  const count=name=>req.rawHeaders.filter((v,i)=>i%2===0&&v.toLowerCase()===name).length;
  try{
   if(count('host')!==1||req.headers.host!==new URL(origin).host||count('origin')>1||req.headers['sec-fetch-site']==='cross-site'||req.headers.origin&&req.headers.origin!==origin)throw fail(403,'request_not_allowed');
   if(!req.url.startsWith('/')||/[?%#]/.test(req.url))throw fail(400,'invalid_path');
   if(req.method==='POST'&&req.headers.origin!==origin)throw fail(403,'origin_required');
   if(req.method==='POST'&&(count('content-type')!==1||req.headers['content-type'].split(';')[0].trim()!=='application/json'))throw fail(415,'json_required');
   if(serveUI&&req.method==='GET'&&['/','/staff-login','/app.js','/app.css',...(extension?['/chat/','/desk/']:[])].includes(req.url)){
    const file=req.url==='/app.js'?'app.js':req.url==='/app.css'?'app.css':'index.html';
    const data=await readFile(new URL('./public/'+file,import.meta.url));res.writeHead(200,{'Content-Type':file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html; charset=utf-8'});res.end(data);return;
   }
   const match=/^\/auth\/(customer|staff)\/(login|logout|me)$/.exec(req.url);
   if(match){
    const [,role,action]=match;
    if(req.method!==(action==='me'?'GET':'POST'))throw fail(405,'method_not_allowed');
    if(action==='me'){const p=identity(req,role);send(res,200,{identity:p?{id:p.subjectID,role,version:p.version}:null});return;}
    let body;try{body=JSON.parse((await bounded(req,8192)).toString());}catch(e){if(e.status)throw e;throw fail(400,'invalid_request');}
    const fields=action==='login'?['account','password']:[];
    if(!body||typeof body!=='object'||Array.isArray(body)||Object.keys(body).some(k=>!fields.includes(k)))throw fail(400,'invalid_request');
    if(action==='logout'){res.setHeader('Set-Cookie',directory.close(req,realm(role)));send(res,204);return;}
    let cookie;try{cookie=await directory.open(req,realm(role),body.account,body.password);}catch(e){if(e.limit)throw fail(429,'login_limit');throw e;}
    if(!cookie)throw fail(401,'login_required');res.setHeader('Set-Cookie',cookie);send(res,200,{authenticated:true});return;
   }
   if(extension&&await extension(req,res,{send,count}))return;
   throw fail(404,'not_found');
  }catch(e){if(!res.headersSent)send(res,e.status||500,{code:e.code||'internal_error'});else res.destroy();}
  finally{active--;}
 });
 server.requestTimeout=25000;server.headersTimeout=5000;server.keepAliveTimeout=5000;server.maxHeadersCount=64;
 try{await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(port,'127.0.0.1',resolve);});}catch(e){directory.dispose();throw e;}
 origin='http://127.0.0.1:'+server.address().port;
 return {origin,sessions:{authenticate:identity},mount(handler){if(extension)throw Error('already mounted');extension=handler;},async close(){directory.dispose();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
