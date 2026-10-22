import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import {startEdge} from './edge.mjs';
import {startBusiness} from '../../integrations/fresh-onboarding/business.mjs';
test('browser assets retain MIME/CSP; internal and secret paths stay closed',async()=>{
 const business=await startBusiness({accounts:[{realm:'member',handle:'test',number:'MEM:100',passphrase:'only-local-test-password'}],serveUI:true});let edge;const audit=[];
 try{
  edge=await startEdge({origin:'http://127.0.0.1:44444',upstreamOrigin:business.origin,serveUI:true,port:0,host:'127.0.0.1',audit:e=>audit.push(e)});
  const call=(path,method='GET',origin='http://127.0.0.1:44444')=>new Promise((resolve,reject)=>{
   const req=http.request({hostname:'127.0.0.1',port:edge.port,path,method,headers:{Host:'127.0.0.1:44444',Origin:origin,'Content-Type':'application/json'}},res=>{let text='';res.on('data',c=>text+=c);res.on('end',()=>resolve({status:res.statusCode,headers:res.headers,text}));});req.on('error',reject);req.end(method==='POST'?'{}':undefined);
  });
  for(const [path,type] of [['/','text/html'],['/staff-login','text/html'],['/app.js','text/javascript'],['/app.css','text/css']]){const r=await call(path);assert.equal(r.status,200);assert.ok(r.headers['content-type'].startsWith(type));assert.match(r.headers['content-security-policy'],/connect-src 'self'/);assert.equal(r.headers['x-content-type-options'],'nosniff');}
  for(const p of ['/internal/chat/sessions','/.env','/app.js.map','/app.js?secret=hidden','/demo/login'])assert.equal((await call(p)).status,404);
  assert.equal((await call('/app.js','POST')).status,405);
  assert.equal((await call('/auth/customer/login','POST','https://other.invalid')).status,403);
  assert.equal((await call('/auth/customer/login','POST')).status,401);
  assert.ok(audit.every(e=>Object.keys(e).join(',')==='method,path,status'));
  assert.ok(!JSON.stringify(audit).includes('secret=hidden'));
 }finally{await edge?.close();await business.close();}
});
test('UI routes are off by default',async()=>{
 const edge=await startEdge({origin:'http://127.0.0.1:44444',upstreamOrigin:'http://127.0.0.1:1',port:0,host:'127.0.0.1'});
 try{const status=await new Promise((resolve,reject)=>{http.get({hostname:'127.0.0.1',port:edge.port,path:'/app.js',headers:{Host:'127.0.0.1:44444'}},res=>{res.resume();resolve(res.statusCode);}).on('error',reject);});assert.equal(status,404);}finally{await edge.close();}
});

test('edge refuses non-loopback or ambiguous origins',async()=>{
 for(const bad of ['https://127.0.0.1:44444','http://example.com:44444','http://127.0.0.1:44444/']){
  await assert.rejects(startEdge({origin:bad,upstreamOrigin:'http://127.0.0.1:1'}),/loopback_origin_required/);
  await assert.rejects(startEdge({origin:'http://127.0.0.1:44444',upstreamOrigin:bad}),/loopback_origin_required/);
 }
});
