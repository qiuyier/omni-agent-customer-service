// Disposable local browser fault harness; never install in CRMEB/public.
import http from 'node:http';
import fs from 'node:fs';
import readline from 'node:readline';
import {fileURLToPath} from 'node:url';
import {resolve,dirname} from 'node:path';
import {lookup} from 'node:dns/promises';

const upstream='http://demo.crmeb.com';
export function allowedRoute(method,url){
 const routes={
 '/api/login':['POST'],'/api/logout':['GET'],'/kefuapi/login':['POST'],'/kefuapi/user/logout':['POST'],
 '/api/omni-chat/session':['POST'],'/api/omni-chat/messages':['GET','POST'],
 '/api/omni-chat/message-receipt':['POST'],'/api/omni-chat/handoff':['POST'],'/api/omni-chat/consultations':['POST'],
 '/api/omni-chat/desk/me':['GET'],'/api/omni-chat/desk/queue':['GET'],'/api/omni-chat/desk/action':['POST']};
 if(routes[url]?.includes(method))return true;
 return method==='GET'&&/^\/omni-chat-test\/(?:index\.html|desk\.html|(?:app|desk)\.js|app\.css)?(?:\?v=[a-f0-9]{16})?$/.test(url);
}
export const shouldFail=(kind,method,path,status)=>status===200&&method==='POST'&&path===(kind==='send'?'/api/omni-chat/messages':'/api/omni-chat/message-receipt');
const html=`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>CRMEB 本机验收登录</title><h1>本机验收登录</h1><p>登录调用真实 CRMEB 测试接口。密码不保存，凭据只保存在本机测试源；正式客服页面不作修改。</p><form id="login"><label>测试身份<select id="realm"><option value="customer">客户</option><option value="staff">客服</option></select></label><p><label>账号<input id="account" autocomplete="off"></label></p><p><label>密码<input id="password" type="password" autocomplete="off"></label></p><button>登录或切换账号</button></form><p id="result" role="status"></p><p><a href="/omni-chat-test/">打开客户页面</a></p><p><a href="/omni-chat-test/desk.html">打开客服工作台</a></p><button id="clear">清除本测试源的浏览器凭据</button><script src="/lab/login.js"></script></html>`;
const loginJS=`document.querySelector('#login').onsubmit=async e=>{e.preventDefault();const staff=document.querySelector('#realm').value==='staff',out=document.querySelector('#result');out.textContent='正在验证';try{const r=await fetch(staff?'/kefuapi/login':'/api/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({account:document.querySelector('#account').value,password:document.querySelector('#password').value})});const x=await r.json();document.querySelector('#password').value='';if(!r.ok||!x.data?.token)throw Error();if(staff)document.cookie=encodeURIComponent('from-crmeb-admin:kefu_token')+'='+encodeURIComponent(x.data.token)+'; Path=/; SameSite=Strict';else localStorage.setItem('LOGIN_STATUS_TOKEN',x.data.token);out.textContent=staff?'客服登录成功':'客户登录成功；其他客户页会失效，请重新连接';}catch{out.textContent='登录失败';}};document.querySelector('#clear').onclick=()=>{localStorage.removeItem('LOGIN_STATUS_TOKEN');document.cookie=encodeURIComponent('from-crmeb-admin:kefu_token')+'=; Path=/; Max-Age=0; SameSite=Strict';document.querySelector('#result').textContent='本测试源凭据已清除';};`;
export async function main(){
 if(!process.argv.includes('--allow-test-writes'))throw Error('Require --allow-test-writes for local test messages and real test logins');
 const config=JSON.parse(fs.readFileSync('/Users/LOCAL_USER/Code/DockerProject/dnmp8/www/CRMEB/crmeb/runtime/omni-chat-deploy/config.json','utf8'));
 if(config.enabled!==true||config.reply_mode!=='fake'||config.human_enabled!==true)throw Error('Require enabled fake/human local instance');
 const ips=await lookup('demo.crmeb.com',{all:true});
 if(!ips.length||ips.some(x=>!['127.0.0.1','::1'].includes(x.address)))throw Error('CRMEB must resolve only to loopback');
 const folder=resolve(dirname(fileURLToPath(import.meta.url)),'../../examples/archive/integration/crmeb',new Date().toISOString().replace(/[:.]/g,'-'));
 fs.mkdirSync(folder,{recursive:true});
 const report={started_at:new Date().toISOString(),passed:null,review_status:'pending_browser_review',external_model_calls:0,reply_mode:'fake',network:[],commands:[]};
 const save=()=>fs.writeFileSync(folder+'/report.json',JSON.stringify(report,null,2)+'\n');
 let origin,failSend=false,failQuery=false;const logins=new Map();
 const server=http.createServer(async(req,res)=>{
  const json=(code,data)=>res.writeHead(code,{'Content-Type':'application/json','Cache-Control':'no-store'}).end(JSON.stringify(data));
  try{
   if(req.headers.host!==new URL(origin).host||(req.headers.origin&&req.headers.origin!==origin)||req.headers['sec-fetch-site']==='cross-site')return json(403,{error:'forbidden'});
   if(req.method==='POST'&&req.headers.origin!==origin)return json(403,{error:'forbidden'});
   if(req.method==='GET'&&['/lab/','/lab/login.js'].includes(req.url)){
    res.writeHead(200,{'Content-Type':req.url.endsWith('.js')?'text/javascript':'text/html; charset=utf-8','Cache-Control':'no-store','Content-Security-Policy':"default-src 'none'; script-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'"}).end(req.url.endsWith('.js')?loginJS:html);return;
   }
   if(!allowedRoute(req.method,req.url))return json(404,{error:'not_found'});
   let size=0;const chunks=[];for await(const chunk of req){size+=chunk.length;if(size>32768)return json(413,{error:'too_large'});chunks.push(chunk);}
   const body=Buffer.concat(chunks),headers={Origin:upstream};
   for(const name of ['content-type','authori-zation','x-omni-chat-token'])if(req.headers[name])headers[name]=req.headers[name];
   const r=await fetch(upstream+req.url,{method:req.method,headers,body:req.method==='GET'?undefined:body,redirect:'manual',signal:AbortSignal.timeout(20000)});
   const data=Buffer.from(await r.arrayBuffer());let result={};try{result=JSON.parse(data);}catch{}
   const entry={method:req.method,path:req.url,status:r.status};
   if(['/api/login','/kefuapi/login'].includes(req.url)&&result.data?.token)logins.set(result.data.token,req.url==='/api/login'?'customer':'staff');
   if(req.url==='/api/omni-chat/session'&&r.status===200){
    if(result.reply_mode!=='fake')return json(503,{error:'mode_changed'});
    entry.conversation_id=result.conversation_id;
   }
   if(req.url==='/api/omni-chat/messages'&&req.method==='POST'){
    const sent=JSON.parse(body);entry.event_id=sent.event_id;entry.content=sent.content;entry.message_id=result.message_id;
   }
   if(req.url==='/api/omni-chat/message-receipt'){
    entry.event_id=JSON.parse(body).event_id;entry.accepted=result.accepted;entry.conversation_id=result.conversation_id;entry.message_id=result.message_id;
   }
   if(req.url==='/api/omni-chat/messages'&&req.method==='GET'){entry.conversation_id=result.conversation_id;entry.conversation_status=result.status;entry.message_ids=result.messages?.map(x=>x.id);}
   if(req.url==='/api/omni-chat/desk/action'){entry.action=JSON.parse(body).action;entry.conversation_id=result.conversation_id;entry.conversation_status=result.status;}
   let inject=false;
   if(failSend&&shouldFail('send',req.method,req.url,r.status)){failSend=false;inject=true;}
   if(failQuery&&shouldFail('query',req.method,req.url,r.status)){failQuery=false;inject=true;}
   if(inject){entry.browser_status=503;entry.failure_injected=true;}
   report.network.push(entry);save();
   if(inject)return json(503,{error:'unavailable'});
   const out={'Cache-Control':'no-store'};
   for(const name of ['content-type','content-security-policy','x-content-type-options','referrer-policy']){const value=r.headers.get(name);if(value)out[name]=value;}
   res.writeHead(r.status,out).end(data);
  }catch{if(!res.headersSent)json(502,{error:'unavailable'});else res.end();}
 });
 await new Promise(r=>server.listen(0,'127.0.0.1',r));origin='http://127.0.0.1:'+server.address().port;
 report.browser_origin=origin;save();console.log(JSON.stringify({origin,login:origin+'/lab/',report:folder+'/report.json',commands:['fail-next-send','fail-next-query','stop']}));
 const lines=readline.createInterface({input:process.stdin});
 for await(const line of lines){
  const c=line.trim();if(c==='stop')break;
  if(c==='fail-next-send')failSend=true;else if(c==='fail-next-query')failQuery=true;else continue;
  report.commands.push(c);save();console.log(c+': armed');
 }
 lines.close();server.close();server.closeAllConnections();
 let cleaned=0;
 for(const [token,realm]of logins){
  try{const r=await fetch(upstream+(realm==='customer'?'/api/logout':'/kefuapi/user/logout'),{method:realm==='customer'?'GET':'POST',headers:{Origin:upstream,'Authori-zation':'Bearer '+token,'Content-Type':'application/json'},body:realm==='customer'?undefined:'{}',signal:AbortSignal.timeout(5000)});if(r.ok)cleaned++;}catch{}
 }
 report.finished_at=new Date().toISOString();report.stopped=true;report.created_logins=logins.size;report.logout_http_successes=cleaned;logins.clear();save();console.log('Proxy stopped; test logins logout attempted, CRMEB services/data retained.');
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url))await main();
