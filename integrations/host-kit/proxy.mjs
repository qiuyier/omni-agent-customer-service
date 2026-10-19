// Same-origin HTTP boundary for the private adapter. Not an arbitrary URL proxy.
const routes=new Map([
 ['/chat/v1/message-receipt',['POST']],
 ['/host/me',['GET']],['/host/chat-session',['POST']],['/chat/v1/messages',['GET','POST']],
 ['/chat/v1/handoff',['POST']],['/chat/v1/consultations',['POST']],['/chat/v1/previous-consultation',['GET']],
 ['/desk/me',['GET']],['/desk/queue',['GET']],['/desk/action',['POST']],
]);
const statuses=new Set([200,201,204,400,401,403,404,405,409,412,413,415,429,500,502,503,504]);
const failure=(status,code)=>Object.assign(Error(code),{status,code});
function localOrigin(value){const u=new URL(value);if(u.protocol!=='http:'||u.hostname!=='127.0.0.1'||!u.port||u.origin!==value)throw Error('loopback origin required');return u;}
async function bounded(stream,limit){let size=0;const chunks=[];if(stream)for await(const chunk of stream){size+=chunk.length;if(size>limit)throw failure(413,'payload_limit');chunks.push(Buffer.from(chunk));}return Buffer.concat(chunks);}
export function createSupportProxy({adapterOrigin,publicOrigin,sessions,now=Date.now,timeoutMs=20000}){
 localOrigin(adapterOrigin);
 if(typeof publicOrigin!=='function'||typeof sessions?.authenticate!=='function'||typeof now!=='function'||!Number.isInteger(timeoutMs)||timeoutMs<1||timeoutMs>120000)throw Error('invalid support proxy configuration');
 async function identity(request,role){
  let p;try{p=await sessions.authenticate(request,role);}catch{throw failure(503,'authentication_unavailable');}
  if(!p||p.role!==role||p.enabled!==true||typeof p.subjectID!=='string'||!p.subjectID||typeof p.sessionID!=='string'||!p.sessionID||!Number.isFinite(p.expiresAt)||p.expiresAt<=now())return null;
  // Copy the snapshot: an identity provider may mutate its internal record later.
  return {id:p.subjectID,session:p.sessionID,version:p.version};
 }
 return async function handle(request,response){
  if(!routes.has(request.url))return false;
  const send=(status,data)=>{response.writeHead(status,{'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store','X-Content-Type-Options':'nosniff'});response.end(status===204?'':JSON.stringify(data));};
  try{
   let origin;try{origin=localOrigin(publicOrigin());}catch{throw failure(503,'support_unavailable');}
   const count=name=>request.rawHeaders.filter((v,i)=>i%2===0&&v.toLowerCase()===name).length;
   if(count('host')!==1||request.headers.host!==origin.host||count('origin')>1||request.headers['sec-fetch-site']==='cross-site'||request.headers.origin&&request.headers.origin!==origin.origin)throw failure(403,'request_not_allowed');
   if(!routes.get(request.url).includes(request.method))throw failure(405,'method_not_allowed');
   if(request.method==='POST'&&request.headers.origin!==origin.origin)throw failure(403,'origin_required');
   if(request.method==='POST'&&(count('content-type')!==1||request.headers['content-type'].split(';')[0].trim()!=='application/json'))throw failure(415,'json_required');
   if(count('authorization')>1)throw failure(400,'invalid_request');
   const role=request.url.startsWith('/desk/')?'staff':'customer',before=await identity(request,role);
   // Headerless API clients remain supported; a supplied header must be exact.
   if(count('x-business-session')&&(count('x-business-session')!==1||!before||typeof before.version!=='string'||!/^[A-Za-z0-9_-]{16,128}$/.test(before.version)||request.headers['x-business-session']!==before.version))throw failure(401,'session_changed');
   const body=request.method==='POST'?await bounded(request,32768):undefined;
   const headers={Origin:adapterOrigin,...(body===undefined?{}:{'Content-Type':'application/json'})};
   if(request.headers.cookie)headers.Cookie=request.headers.cookie;
   if(request.headers.authorization)headers.Authorization=request.headers.authorization;
   let upstream,data;
   try{
    upstream=await fetch(adapterOrigin+request.url,{method:request.method,headers,body,redirect:'error',signal:AbortSignal.timeout(timeoutMs)});
    if(!statuses.has(upstream.status)||!upstream.headers.get('content-type')?.startsWith('application/json')){await upstream.body?.cancel();throw Error();}
    data=JSON.parse((await bounded(upstream.body,1048576)).toString());
   }catch{throw failure(502,'support_unavailable');}
   const after=await identity(request,role);
   if(before?.id!==after?.id||before?.session!==after?.session||before?.version!==after?.version)throw failure(401,'session_changed');
   send(upstream.status,data);
  }catch(error){
   const known=['payload_limit','authentication_unavailable','support_unavailable','request_not_allowed','method_not_allowed','origin_required','json_required','invalid_request','session_changed'];
   if(!response.headersSent)send(known.includes(error.code)?error.status:500,{code:known.includes(error.code)?error.code:'internal_error'});else response.destroy();
  }
  return true;
 };
}
