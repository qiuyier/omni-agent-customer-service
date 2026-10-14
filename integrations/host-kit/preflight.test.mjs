import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {spawn} from 'node:child_process';
import {preflightHost,preflightExitCode} from './preflight.mjs';
const base={origin:'http://127.0.0.1:32123',paths:{customerIdentity:'/identity/customer',staffIdentity:'/identity/staff'}};
const sessions={customer:{cookie:'member=customer-private-token',expectedID:'member-1'},staff:{cookie:'operator=staff-private-token',expectedID:'staff-1'},revokedCustomer:{cookie:'member=revoked-private-token',expectedID:'member-1'},revokedStaff:{cookie:'operator=revoked-staff-token',expectedID:'staff-1'},unassignedStaff:{cookie:'operator=unassigned-private-token',expectedID:'employee-9'}};
function fixture(url,options={}){
 const path=new URL(url).pathname,cookie=options.headers?.Cookie;
 const who=cookie===sessions.customer.cookie?{id:'member-1',role:'customer',version:'v'.repeat(32)}:cookie===sessions.staff.cookie?{id:'staff-1',role:'staff',version:'s'.repeat(32)}:cookie===sessions.unassignedStaff.cookie?{id:'employee-9',role:'staff',version:'u'.repeat(32)}:null;
 let status=200,data;
 if(options.headers?.['X-Business-Session']&&options.headers['X-Business-Session']!==who?.version){status=401;data={code:'session_changed'};}
 else if(path.startsWith('/identity/'))data={identity:who?.role===path.split('/').at(-1)?who:null};
 else if(path==='/host/me')data={user:who?.role==='customer'?{id:who.id}:null,demo:false,reply_mode:'fake',storage:'memory',human_enabled:true};
 else if(path==='/desk/me')data={agent:who?.id==='staff-1'?{id:who.id}:null};
 else if(path==='/desk/queue'){if(who?.id==='staff-1')data={items:[]};else{status=401;data={code:'desk_login_required'};}}
 else {status=404;data={code:'not_found'};}
 return new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json','Cache-Control':'no-store'}});
}
test('invalid configuration fails before any network request and reports no input values',async()=>{
 let calls=0;const fetcher=()=>{calls++;throw Error();};
 for(const config of [{},{...base,origin:'https://example.com/private-secret'},{...base,origin:'http://user:password@127.0.0.1:12'},{...base,origin:'http://localhost:32123'},{...base,paths:{...base.paths,me:'//other.invalid'}},{...base,paths:{...base.paths,me:'/a/../login'}},{...base,paths:{...base.paths,me:'/x?secret=abc'}},{...base,allowTestWrites:true},{...base,sessions:{customer:{cookie:'x=bad\nheader',expectedID:'id'}}}]){
  const result=await preflightHost(config,{fetcher});assert.equal(result.status,'fail');assert.equal(preflightExitCode(result),1);assert.ok(!JSON.stringify(result).includes('private-secret'));
 }assert.equal(calls,0);
});
test('anonymous default uses GET only and leaves authenticated and mutation checks unverified',async()=>{
 const calls=[];const report=await preflightHost(base,{fetcher:async(url,options)=>{calls.push({url,options});return fixture(url,options);}});
 assert.equal(report.status,'needs_verification');assert.equal(preflightExitCode(report),2);assert.equal(calls.length,5);
 assert.ok(calls.every(c=>c.options.method==='GET'&&c.options.body===undefined&&!c.options.headers.Cookie&&c.options.redirect==='error'));
 assert.ok(report.checks.some(c=>c.id==='customer.session'&&c.status==='needs_verification'));
 assert.ok(!report.checks.some(c=>c.status==='fail'));
});
test('provided sessions check role, authorization, stale versions and externally revoked identities without writes',async()=>{
 const calls=[];const report=await preflightHost({...base,sessions},{fetcher:async(url,options)=>{calls.push({url,options});return fixture(url,options);}});
 assert.equal(report.status,'needs_verification');assert.ok(!report.checks.some(c=>c.status==='fail'),JSON.stringify(report));
 for(const id of ['staff.queue.allowed','customer.queue.denied','customer.stale_version.denied','revokedCustomer.denied','revokedStaff.denied','unassigned.queue.denied'])assert.equal(report.checks.find(c=>c.id===id)?.status,'pass');
 assert.ok(calls.every(c=>c.options.method==='GET'&&c.options.body===undefined));
 for(const value of Object.values(sessions))assert.ok(!JSON.stringify(report).includes(value.cookie.split('=')[1]));
});
test('wrong roles, account mapping and unauthorized queue success are failures',async()=>{
 const report=await preflightHost({...base,sessions},{fetcher:async(url,options)=>{
  const path=new URL(url).pathname;
  if(path==='/identity/customer'&&options.headers.Cookie===sessions.customer.cookie)return new Response(JSON.stringify({identity:{id:'staff-1',role:'staff',version:'x'.repeat(32)}}),{headers:{'Content-Type':'application/json','Cache-Control':'no-store'}});
  if(path==='/desk/queue')return new Response('{"items":[]}',{headers:{'Content-Type':'application/json','Cache-Control':'no-store'}});
  return fixture(url,options);
 }});
 assert.equal(report.status,'fail');assert.equal(report.checks.find(c=>c.id==='customer.identity.schema').status,'fail');assert.equal(report.checks.find(c=>c.id==='anonymous.queue.denied').status,'fail');
});
test('a still-active supposedly revoked cookie is not reported as revoked',async()=>{
 const report=await preflightHost({...base,sessions:{revokedCustomer:sessions.customer}},{fetcher:async(...args)=>fixture(...args)});
 assert.equal(report.status,'fail');assert.equal(report.checks.find(c=>c.id==='revokedCustomer.denied').status,'fail');
});
test('known credential echoes and error stacks are detected but never copied into report',async()=>{
 const report=await preflightHost({...base,sessions},{fetcher:async()=>new Response(JSON.stringify({code:'customer-private-token',stack:'secret-stack-location'}),{status:500,headers:{'Content-Type':'application/json','Cache-Control':'no-store'}})});
 assert.equal(report.status,'fail');assert.ok(report.checks.some(c=>c.id.endsWith('.privacy')&&c.status==='fail'));
 assert.ok(!JSON.stringify(report).includes('customer-private-token'));assert.ok(!JSON.stringify(report).includes('secret-stack-location'));
});
test('missing no-store and malformed JSON are clear failures',async()=>{
 const report=await preflightHost(base,{fetcher:async(url,options)=>{const response=fixture(url,options);response.headers.delete('Cache-Control');return response;}});
 assert.equal(report.checks.find(c=>c.id==='anonymous.customer.cache').status,'fail');
 const invalid=await preflightHost(base,{fetcher:async()=>new Response('<html>private server exception</html>',{headers:{'Content-Type':'text/html'}})});
 assert.equal(invalid.status,'fail');assert.ok(!JSON.stringify(invalid).includes('private server exception'));
 const wrongType=await preflightHost(base,{fetcher:async()=>new Response('{}',{headers:{'Content-Type':'application/jsonp'}})});assert.equal(wrongType.status,'fail');
});
test('redirects, oversized bodies and thrown network errors are sanitized',async()=>{
 for(const fetcher of [async()=>new Response(null,{status:302,headers:{Location:'http://foreign.invalid'}}),async()=>new Response('x'.repeat(65537),{headers:{'Content-Type':'application/json'}}),async()=>{throw Error('private network detail');}]){
  const report=await preflightHost(base,{fetcher});assert.equal(report.status,'fail');assert.ok(report.checks.some(c=>c.id.endsWith('.transport')&&c.status==='fail'));assert.ok(!JSON.stringify(report).includes('private network detail'));
 }
});
test('real HTTP CLI only reads configured routes, emits JSON and exits 2 for incomplete coverage',async t=>{
 const calls=[];const server=http.createServer(async(req,res)=>{calls.push({method:req.method,path:req.url});const response=fixture('http://127.0.0.1'+req.url,{});res.writeHead(response.status,Object.fromEntries(response.headers));res.end(await response.text());});
 await new Promise(r=>server.listen(0,'127.0.0.1',r));t.after(()=>{server.closeAllConnections();return new Promise(r=>server.close(r));});
 const child=spawn(process.execPath,[new URL('./preflight.mjs',import.meta.url).pathname],{stdio:['pipe','pipe','pipe']});let output='',errors='';child.stdout.on('data',c=>output+=c);child.stderr.on('data',c=>errors+=c);const done=new Promise((r,j)=>{child.once('error',j);child.once('close',r);});child.stdin.end(JSON.stringify({...base,origin:'http://127.0.0.1:'+server.address().port}));
 assert.equal(await done,2);assert.equal(errors,'');assert.equal(JSON.parse(output).status,'needs_verification');assert.equal(calls.length,5);assert.ok(calls.every(c=>c.method==='GET'));
});
test('unreachable loopback service and real read timeout cannot pass',async t=>{
 const server=http.createServer((req,res)=>{});await new Promise(r=>server.listen(0,'127.0.0.1',r));t.after(()=>{server.closeAllConnections();return new Promise(r=>server.close(r));});
 const report=await preflightHost({...base,origin:'http://127.0.0.1:'+server.address().port},{timeoutMs:30});assert.equal(report.status,'fail');
});
