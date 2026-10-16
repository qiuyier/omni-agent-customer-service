// The ONLY support-specific backend glue. Original business auth stays unchanged.
import {startAdapter} from '../host-kit/adapter.mjs';
import {businessIdentity} from '../host-kit/identity.example.mjs';
import {bounded,fail} from './business.mjs';
const routes=new Map([
 ['/host/me',['GET']],['/host/chat-session',['POST']],['/chat/v1/messages',['GET','POST']],
 ['/chat/v1/handoff',['POST']],['/chat/v1/consultations',['POST']],['/chat/v1/previous-consultation',['GET']],
 ['/desk/me',['GET']],['/desk/queue',['GET']],['/desk/action',['POST']],
]);
export async function attachSupport(business,{gatewayOrigin,hostSecret,agents}){
 const adapter=await startAdapter({gatewayOrigin,hostSecret,agents,storage:'memory',replyMode:'fake',identity:businessIdentity({sessions:business.sessions})});
 try{business.mount(async(req,res,{send,count})=>{
  if(!routes.has(req.url))return false;
  if(!routes.get(req.url).includes(req.method))throw fail(405,'method_not_allowed');
  const role=req.url.startsWith('/desk/')?'staff':'customer',before=await business.sessions.authenticate(req,role);
  // Version is public, not authentication; cookie checks still happen on both sides.
  if(count('x-business-session')&&(count('x-business-session')!==1||!before||req.headers['x-business-session']!==before.version))throw fail(401,'session_changed');
  if(count('authorization')>1)throw fail(400,'invalid_request');
  const body=req.method==='POST'?await bounded(req,32768):undefined;
  const headers={Origin:adapter.origin,...(body===undefined?{}:{'Content-Type':'application/json'})};
  if(req.headers.cookie)headers.Cookie=req.headers.cookie;if(req.headers.authorization)headers.Authorization=req.headers.authorization;
  let upstream,data;
  try{upstream=await fetch(adapter.origin+req.url,{method:req.method,headers,body,redirect:'error',signal:AbortSignal.timeout(20000)});
   if(!upstream.headers.get('content-type')?.startsWith('application/json'))throw Error();
   data=JSON.parse((await bounded(upstream.body,1048576)).toString());
  }catch{throw fail(502,'support_unavailable');}
  const after=await business.sessions.authenticate(req,role);
  if(before?.sessionID!==after?.sessionID||before?.subjectID!==after?.subjectID)throw fail(401,'session_changed');
  send(res,upstream.status,data);return true;
 });}catch(e){await adapter.close();throw e;}
 return {close:()=>adapter.close()};
}
