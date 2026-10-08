// Independent business backend. No CRMEB dependency and no Go core imports.
// Local-only account selection is deliberately NOT production authentication.
import http from 'node:http';
import {randomBytes, createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';

const assets = fileURLToPath(new URL('../../internal/webchat/ui/', import.meta.url));
const cookieName = 'standalone_login';
const digest = value => createHash('sha256').update(value).digest('hex');
const users = {lin:{id:'member-1001',display_name:'林澈'},zhou:{id:'member-1002',display_name:'周宁'}};
const failure = (status,code) => Object.assign(Error(code),{status,code});
const routes = new Map([['/','index.html'],['/chat/','index.html'],['/assets/app.js','assets/app.js'],['/assets/app.css','assets/app.css']]);

async function boundedBody(stream, limit) {
  let size=0;const chunks=[];
  for await (const data of stream) {size+=data.length;if(size>limit)throw failure(413,'request_too_large');chunks.push(data);}
  return Buffer.concat(chunks);
}
function strictObject(value, fields) {
  if(!value || Array.isArray(value) || typeof value!=='object' || Object.keys(value).some(k=>!fields.includes(k)))throw failure(400,'invalid_request');
  return value;
}
export async function startHost({gatewayOrigin,hostSecret,now=Date.now,storage='memory',port=0}) {
  if(!['memory','postgres'].includes(storage)||!Number.isInteger(port)||port<0||port>65535)throw Error('invalid trusted local host configuration');
  const metadata={demo:true,storage,reply_mode:'fake'};
  const upstream=new URL(gatewayOrigin);
  if(upstream.protocol!=='http:'||upstream.hostname!=='127.0.0.1'||!upstream.port||upstream.pathname!=='/'||upstream.search||upstream.hash||upstream.username||upstream.password)throw Error('private loopback gateway required');
  if(!/^[A-Za-z0-9_-]{32,128}$/.test(hostSecret)||hostSecret.startsWith('chat_'))throw Error('independent host credential required');
  const sessions=new Map();let origin,active=0,loginIssues=0,loginWindow=now();
  const respond=(res,status,data)=>{res.writeHead(status,{'Content-Type':'application/json; charset=utf-8'});res.end(data===undefined?'':JSON.stringify(data));};
  function loginSession(req) {
    for(const [key,s] of sessions)if(s.expires<=now())sessions.delete(key);
    const cookies=(req.headers.cookie||'').split(';').map(v=>v.trim()).filter(v=>v.startsWith(cookieName+'='));
    if(cookies.length!==1)return null;
    const token=cookies[0].slice(cookieName.length+1);
    if(!/^[A-Za-z0-9_-]{43}$/.test(token))return null;
    const key=digest(token),value=sessions.get(key);
    return value?{key,value}:null;
  }
  function current(login) {return login&&sessions.get(login.key)===login.value&&login.value.expires>now();}
  async function remote(path,method,token,body) {
    let response;
    try {
      response=await fetch(upstream.origin+path,{method,headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),redirect:'manual',signal:AbortSignal.timeout(15000)});
      if(!response.headers.get('content-type')?.startsWith('application/json'))throw Error('invalid type');
      const data=JSON.parse((await boundedBody(response.body,1<<20)).toString());
      if(![200,201].includes(response.status))throw failure([400,401,403,409,412,429,503,504].includes(response.status)?response.status:502,'gateway_unavailable');
      return data;
    }catch(e){if(e.code==='gateway_unavailable')throw e;throw failure(502,'gateway_unavailable');}
  }
  const server=http.createServer(async(req,res)=>{
    res.setHeader('Cache-Control','no-store');res.setHeader('X-Content-Type-Options','nosniff');res.setHeader('Referrer-Policy','no-referrer');
    res.setHeader('Content-Security-Policy',"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'");
    if(active>=16){respond(res,429,{code:'busy'});return;}active++;
    try {
      const count=name=>req.rawHeaders.filter((_,i)=>i%2===0&&req.rawHeaders[i].toLowerCase()===name).length;
      if(req.headers.host!==new URL(origin).host||count('host')!==1||req.headers['sec-fetch-site']==='cross-site'||count('origin')>1||(req.headers.origin&&req.headers.origin!==origin))throw failure(403,'request_not_allowed');
      if(!req.url.startsWith('/')||req.url.includes('?')||req.url.includes('%')||req.url.includes('#'))throw failure(400,'invalid_path');
      if(req.method==='POST'&&(req.headers.origin!==origin))throw failure(403,'origin_required');
      if(req.method==='POST'&&(count('content-type')!==1||req.headers['content-type'].split(';')[0].trim()!=='application/json'))throw failure(415,'json_required');
      const url=req.url;
      if(req.method==='GET'&&routes.has(url)) {
        const data=await readFile(assets+routes.get(url));
        res.writeHead(200,{'Content-Type':url.endsWith('.js')?'text/javascript':url.endsWith('.css')?'text/css':'text/html; charset=utf-8'});res.end(data);return;
      }
      const allowed={'/host/me':['GET'],'/demo/login':['POST'],'/demo/logout':['POST'],'/host/chat-session':['POST'],'/chat/v1/messages':['GET','POST']};
      if(!Object.hasOwn(allowed,url))throw failure(404,'not_found');
      if(!allowed[url].includes(req.method))throw failure(405,'method_not_allowed');
      let body;
      if(req.method==='POST') {try{body=JSON.parse((await boundedBody(req,32768)).toString());}catch(e){if(e.status)throw e;throw failure(400,'invalid_request');}}
      const login=loginSession(req);
      if(url==='/host/me'){respond(res,200,{user:login?.value.user||null,...metadata});return;}
      if(url==='/demo/login') {
        strictObject(body,['account']);if(typeof body.account!=='string'||!Object.hasOwn(users,body.account))throw failure(400,'unknown_demo_account');
        if(now()-loginWindow>=3600000){loginWindow=now();loginIssues=0;}
        if(++loginIssues>128||sessions.size>=128)throw failure(429,'login_limit');
        if(login)sessions.delete(login.key);
        const raw=randomBytes(32).toString('base64url');
        sessions.set(digest(raw),{user:users[body.account],expires:now()+1800000,cap:null,minting:false,issues:0});
        res.setHeader('Set-Cookie',`${cookieName}=${raw}; Path=/; HttpOnly; SameSite=Strict; Max-Age=1800`);
        respond(res,200,{user:users[body.account],...metadata});return;
      }
      if(url==='/demo/logout') {
        strictObject(body,[]);if(login)sessions.delete(login.key);
        res.setHeader('Set-Cookie',`${cookieName}=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0`);respond(res,204);return;
      }
      if(!current(login))throw failure(401,'host_login_required');
      const s=login.value;
      if(url==='/host/chat-session') {
        strictObject(body,[]);
        if(s.cap&&Date.parse(s.cap.expires_at)>now()+30000){respond(res,200,{...s.cap,user:s.user,...metadata});return;}
        if(s.minting||s.issues>=32)throw failure(429,'session_limit');
        s.minting=true;s.issues++;
        try {
          const cap=await remote('/internal/chat/sessions','POST',hostSecret,{customer_id:s.user.id});
          if(!current(login))throw failure(401,'host_login_required');
          strictObject(cap,['access_token','expires_at','conversation_id']);
          const expires=Date.parse(cap.expires_at);
          if(!/^chat_[A-Za-z0-9_-]{43}$/.test(cap.access_token)||!/^wbc_[a-f0-9]{64}$/.test(cap.conversation_id)||!Number.isFinite(expires)||expires<=now()||expires>now()+600000)throw failure(502,'gateway_unavailable');
          s.cap={...cap,expires_at:new Date(Math.min(expires,s.expires)).toISOString()};
          respond(res,200,{...s.cap,user:s.user,...metadata});return;
        } finally {s.minting=false;}
      }
      if(count('authorization')!==1||!s.cap||Date.parse(s.cap.expires_at)<=now()||req.headers.authorization!=='Bearer '+s.cap.access_token)throw failure(401,'chat_session_required');
      const cap=s.cap;
      if(req.method==='POST') {
        strictObject(body,['event_id','content']);
        if(typeof body.event_id!=='string'||typeof body.content!=='string'||!body.content.trim()||Buffer.byteLength(body.content)>4000)throw failure(400,'invalid_request');
      }
      let data;
      try {data=await remote('/chat/v1/messages',req.method,cap.access_token,body);}
      catch(e){if(e.status===401&&s.cap===cap)s.cap=null;throw e;}
      // Logout cannot undo an admitted send, but must suppress its late response.
      if(!current(login)||s.cap!==cap||Date.parse(cap.expires_at)<=now())throw failure(401,'chat_session_required');
      respond(res,200,data);
    }catch(e){if(!res.headersSent)respond(res,e.status||500,{code:e.code||'internal_error'});else res.end();}
    finally{active--;}
  });
  server.requestTimeout=20000;server.headersTimeout=5000;server.keepAliveTimeout=5000;server.maxHeadersCount=64;
  await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(port,'127.0.0.1',resolve);});
  origin=`http://127.0.0.1:${server.address().port}`;
  return {origin,close:async()=>{sessions.clear();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
