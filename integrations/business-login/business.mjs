// Local test business login, not production IAM. No default credentials; browser UI is opt-in.
import http from 'node:http';
import {readFile} from 'node:fs/promises';
import {randomBytes,createHash,scryptSync,timingSafeEqual} from 'node:crypto';
import {startAdapter} from '../host-kit/adapter.mjs';
import {businessIdentity} from '../host-kit/identity.example.mjs';
const hash=value=>createHash('sha256').update(value).digest('hex');
const fail=status=>Object.assign(Error('request_rejected'),{status});
async function read(stream,limit){let size=0;const chunks=[];for await(const chunk of stream){size+=chunk.length;if(size>limit)throw fail(413);chunks.push(Buffer.from(chunk));}return Buffer.concat(chunks);}
export async function startBusiness({gatewayOrigin,hostSecret,agents,accounts,now=Date.now,serveUI=false,storage='memory',replyMode='fake'}){
 const people=new Map(),sessions=new Map();
 for(const a of accounts){
  if(!['customer','staff'].includes(a.role)||typeof a.password!=='string'||a.password.length<16||typeof a.account!=='string'||!/^[A-Za-z0-9_-]{1,128}$/.test(a.id))throw Error('invalid test account');
  const key=a.role+':'+a.account;if(people.has(key))throw Error('duplicate test account');
  const salt=randomBytes(16);people.set(key,{id:a.id,role:a.role,enabled:true,salt,digest:scryptSync(a.password,salt,32)});
 }
 const cookieName=role=>'repair_'+role;
 function session(req,role){
  const found=(req.headers.cookie||'').split(';').map(x=>x.trim()).filter(x=>x.startsWith(cookieName(role)+'='));
  if(found.length!==1)return null;const token=found[0].slice(cookieName(role).length+1);if(!/^[A-Za-z0-9_-]{43}$/.test(token))return null;
  const key=hash(token),s=sessions.get(key);
  // An incorrect realm must not revoke a valid login belonging to another role.
  if(!s||s.role!==role)return null;
  if(s.expiresAt<=now()||!people.get(s.accountKey)?.enabled){sessions.delete(key);return null;}
  return {key,...s};
 }
 const identityService={authenticate(req,role){
  const s=session(req,role);if(!s)return null;
  const a=people.get(s.accountKey);return {role,enabled:a.enabled,subjectID:a.id,displayName:a.id,sessionID:s.key,expiresAt:s.expiresAt};
 }};
 const adapter=await startAdapter({gatewayOrigin,hostSecret,agents,identity:businessIdentity({sessions:identityService}),now,storage,replyMode});
 const allowed=new Map([['/host/me','GET'],['/host/chat-session','POST'],['/chat/v1/messages','GET,POST'],['/chat/v1/handoff','POST'],['/chat/v1/consultations','POST'],['/chat/v1/previous-consultation','GET'],['/desk/me','GET'],['/desk/queue','GET'],['/desk/action','POST']]);
 allowed.set('/chat/v1/message-receipt','POST');
 let origin,attempts=0;
 const respond=(res,status,data)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(data===undefined?'':JSON.stringify(data));};
 const server=http.createServer(async(req,res)=>{
  res.setHeader('Cache-Control','no-store');res.setHeader('X-Content-Type-Options','nosniff');
  try{
   const count=name=>req.rawHeaders.filter((v,i)=>i%2===0&&v.toLowerCase()===name).length;
   if(req.headers.host!==new URL(origin).host||count('host')!==1||count('origin')>1||req.headers['sec-fetch-site']==='cross-site'||(req.headers.origin&&req.headers.origin!==origin)||(req.method==='POST'&&req.headers.origin!==origin))throw fail(403);
   if(serveUI&&req.method==='GET'){
    const pages=['/','/staff-login','/chat/','/desk/'];
    const asset=new Map([['/business.js','app.js'],['/business.css','app.css']]).get(req.url);
    if(pages.includes(req.url)||asset){
     const data=await readFile(new URL('./public/'+(asset||'index.html'),import.meta.url));
     res.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'");
     res.writeHead(200,{'Content-Type':asset?.endsWith('.js')?'text/javascript':asset?.endsWith('.css')?'text/css':'text/html; charset=utf-8'});res.end(data);return;
    }
   }
   const me=/^\/auth\/(customer|staff)\/me$/.exec(req.url);
   if(me){if(req.method!=='GET')throw fail(405);const current=session(req,me[1]);respond(res,200,{identity:current?{id:people.get(current.accountKey).id,role:me[1],version:current.version}:null});return;}
   if(req.method==='POST'&&(count('content-type')!==1||req.headers['content-type'].split(';')[0]!=='application/json'))throw fail(415);
   const auth=/^\/auth\/(customer|staff)\/(login|logout)$/.exec(req.url);
   if(auth){
    if(req.method!=='POST')throw fail(405);
    const body=JSON.parse((await read(req,4096)).toString());if(!body||typeof body!=='object'||Array.isArray(body))throw fail(400);
    const [,role,action]=auth,old=session(req,role);
    if(action==='logout'){
     if(Object.keys(body).length)throw fail(400);
     if(old)sessions.delete(old.key);
     res.setHeader('Set-Cookie',cookieName(role)+'=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0');respond(res,204);return;
    }
    if(Object.keys(body).some(k=>!['account','password'].includes(k))||typeof body.account!=='string'||typeof body.password!=='string'||body.password.length>256)throw fail(400);
    if(++attempts>128)throw fail(429);
    const accountKey=role+':'+body.account,a=people.get(accountKey);
    const actual=scryptSync(body.password,a?.salt||Buffer.alloc(16),32);
    if(!a||!timingSafeEqual(actual,a.digest)||!a.enabled)throw fail(401);
    for(const [k,v]of sessions)if(v.expiresAt<=now())sessions.delete(k);
    if(sessions.size>=128)throw fail(429);
    if(old)sessions.delete(old.key);
    const raw=randomBytes(32).toString('base64url');sessions.set(hash(raw),{accountKey,role,version:randomBytes(16).toString('hex'),expiresAt:now()+1800000});
    res.setHeader('Set-Cookie',cookieName(role)+'='+raw+'; Path=/; HttpOnly; SameSite=Strict; Max-Age=1800');
    respond(res,200,{user:{id:a.id,role}});return;
   }
   if(!allowed.has(req.url))throw fail(404);
   if(!allowed.get(req.url).split(',').includes(req.method))throw fail(405);
   if(count('x-business-session')){
    const current=session(req,req.url.startsWith('/desk/')?'staff':'customer');
    if(count('x-business-session')!==1||!current||req.headers['x-business-session']!==current.version)throw fail(401);
   }
   const headers={Origin:adapter.origin};
   if(req.headers.cookie)headers.Cookie=req.headers.cookie;
   if(req.headers.authorization){if(count('authorization')!==1)throw fail(401);headers.Authorization=req.headers.authorization;}
   let body;if(req.method==='POST'){headers['Content-Type']='application/json';body=await read(req,32768);}
   const response=await fetch(adapter.origin+req.url,{method:req.method,headers,body,redirect:'error',signal:AbortSignal.timeout(15000)});
   const data=await read(response.body,1048576);
   res.writeHead(response.status,{'Content-Type':'application/json'});res.end(data);
  }catch(e){respond(res,e.status||400,{code:'request_rejected'});}
 });
 server.requestTimeout=20000;server.headersTimeout=5000;server.maxHeadersCount=64;
 try{await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});}catch(e){await adapter.close();throw e;}
 origin='http://127.0.0.1:'+server.address().port;
 return {origin,
  // Private test harness control, deliberately NOT an HTTP administration API.
  setEnabled(role,account,enabled){const key=role+':'+account,a=people.get(key);if(!a||typeof enabled!=='boolean')throw Error('unknown test account');a.enabled=enabled;if(!enabled)for(const [k,s]of sessions)if(s.accountKey===key)sessions.delete(k);},
  async close(){server.closeAllConnections();await new Promise(r=>server.close(r));await adapter.close();sessions.clear();people.clear();}
 };
}
