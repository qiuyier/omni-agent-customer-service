import http from 'node:http';
export async function startEdge({origin,upstreamOrigin,serveUI=false,port=8080,host='0.0.0.0',audit,beforeForward,afterResponse}){
const publicURL=new URL(origin),upstreamURL=new URL(upstreamOrigin);
for(const [value,url] of [[origin,publicURL],[upstreamOrigin,upstreamURL]])if(url.protocol!=='http:'||url.hostname!=='127.0.0.1'||!url.port||url.origin!==value)throw Error('loopback_origin_required');
const paths=new Set(['/auth/customer/login','/auth/customer/logout','/auth/customer/me','/auth/staff/login','/auth/staff/logout','/auth/staff/me','/host/me','/host/chat-session','/chat/v1/messages','/chat/v1/message-receipt','/chat/v1/handoff','/chat/v1/consultations','/chat/v1/previous-consultation','/desk/me','/desk/queue','/desk/action']);
const assets=new Set(serveUI?['/','/staff-login','/chat/','/desk/','/app.js','/app.css']:[]);
const edge=http.createServer(async(req,res)=>{
 res.once('finish',()=>audit?.({method:req.method,path:paths.has(req.url)||assets.has(req.url)?req.url:'[rejected]',status:res.statusCode}));
 const fail=status=>res.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'}).end('{"error":"unavailable"}');
 try{
  const count=name=>req.rawHeaders.filter((v,i)=>i%2===0&&v.toLowerCase()===name).length;
  if(req.headers.host!==publicURL.host||count('host')!==1||count('origin')>1||req.headers['sec-fetch-site']==='cross-site'||(req.headers.origin&&req.headers.origin!==publicURL.origin)||(req.method==='POST'&&req.headers.origin!==publicURL.origin))return fail(403);
  if(!paths.has(req.url)&&!assets.has(req.url))return fail(404);
  if(assets.has(req.url)&&req.method!=='GET')return fail(405);
  if(!['GET','POST'].includes(req.method))return fail(405);
  const headers={Origin:upstreamOrigin};
  for(const name of ['cookie','content-type','authorization','x-business-session']){if(count(name)>1)return fail(400);if(req.headers[name])headers[name]=req.headers[name];}
  const chunks=[];let size=0;for await(const chunk of req){size+=chunk.length;if(size>32768)return fail(413);chunks.push(chunk);}
  if(req.method==='GET'&&size)return fail(400);
  if(beforeForward&&!await beforeForward(req,res)){if(!res.destroyed)fail(503);return;}
  if(res.destroyed)return;
  const r=await fetch(upstreamOrigin+req.url,{method:req.method,headers,body:req.method==='POST'?Buffer.concat(chunks):undefined,redirect:'error',signal:AbortSignal.timeout(13000)});
  const data=Buffer.from(await r.arrayBuffer());if(data.length>1048576)return fail(502);
  if(afterResponse&&!await afterResponse(req,r.status)){res.destroy();return;}
  const out={'Content-Type':r.headers.get('content-type')||'application/json','Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Referrer-Policy':'no-referrer','Content-Security-Policy':"default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"};const cookie=r.headers.getSetCookie();if(cookie.length)out['Set-Cookie']=cookie;
  res.writeHead(r.status,out).end(data);
 }catch{if(!res.headersSent)fail(503);else res.destroy();}
});
edge.headersTimeout=5000;edge.requestTimeout=15000;edge.maxHeadersCount=32;
await new Promise((r,j)=>{edge.once('error',j);edge.listen(port,host,r);});
return {port:edge.address().port,close:async()=>{edge.closeAllConnections();await new Promise(r=>edge.close(r));}};
}
