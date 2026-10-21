import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import https from 'node:https';
import {mkdtemp,writeFile,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {X509Certificate} from 'node:crypto';
import {permitted,startClient,startServer} from './transport.mjs';
test('exact route/method allowlist and fixed client target',()=>{
 assert.equal(permitted('POST','/internal/chat/sessions'),true);
 for(const path of ['/internal/chat/sessions?x=1','//internal/chat/sessions','/internal/chat/sessions/','/readyz','/internal/chat/%73essions'])assert.equal(permitted('POST',path),false);
 assert.equal(permitted('GET','/internal/chat/sessions'),false);
 for(const origin of ['http://gateway:8443','https://example.com:8443','https://gateway:8443/','https://user@gateway:8443'])assert.throws(()=>startClient({origin}));
});
test('mTLS verifies trust, pins peer, strips cookies and fails closed',async()=>{
 const dir=await mkdtemp(join(tmpdir(),'omni-transport-unit-')),run=promisify(execFile);let server,upstream;
 try{
  const ssl=(...a)=>run('openssl',a,{cwd:dir});
  await ssl('req','-x509','-newkey','rsa:2048','-nodes','-keyout','ca.key','-out','ca.crt','-days','1','-subj','/CN=unit-ca');
  for(const name of ['server','client','other']){
   await ssl('req','-newkey','rsa:2048','-nodes','-keyout',name+'.key','-out',name+'.csr','-subj','/CN='+name);
   await writeFile(join(dir,name+'.ext'),'extendedKeyUsage='+(name==='server'?'serverAuth\nsubjectAltName=DNS:gateway':'clientAuth')+'\n');
   await ssl('x509','-req','-in',name+'.csr','-CA','ca.crt','-CAkey','ca.key','-CAcreateserial','-out',name+'.crt','-days','1','-extfile',name+'.ext');
  }
  let hits=0,seen;
  upstream=http.createServer((req,res)=>{hits++;seen=req.headers;req.resume();req.on('end',()=>res.writeHead(200,{'Content-Type':'application/json','Set-Cookie':'private=hidden'}).end('{"ok":true}'));});
  await new Promise(r=>upstream.listen(0,'127.0.0.1',r));
  const material={};for(const name of ['ca.crt','client.crt','client.key','other.crt','other.key'])material[name]=await readFile(join(dir,name));
  server=await startServer({ca:join(dir,'ca.crt'),cert:join(dir,'server.crt'),key:join(dir,'server.key'),clientFingerprint:new X509Certificate(material['client.crt']).fingerprint256,port:0,upstreamPort:upstream.address().port});
  const request=(peer='client',path='/internal/chat/sessions',auth='Bearer '+'x'.repeat(43))=>new Promise((resolve,reject)=>{
   const r=https.request({hostname:'127.0.0.1',port:server.port,servername:'gateway',ca:material['ca.crt'],...(peer?{cert:material[peer+'.crt'],key:material[peer+'.key']}:{}),method:'POST',path,agent:false,headers:{Authorization:auth,'Content-Type':'application/json',Cookie:'must-not-forward=yes'}},res=>{res.resume();res.on('end',()=>resolve({status:res.statusCode,headers:res.headers}));});r.on('error',reject);r.end('{}');
  });
  await assert.rejects(request(null));assert.equal(hits,0);
  assert.equal((await request('other')).status,403);assert.equal(hits,0);
  assert.equal((await request('client','/readyz')).status,404);
  assert.equal((await request('client',undefined,'invalid')).status,401);
  const ok=await request();assert.equal(ok.status,200);assert.equal(hits,1);assert.equal(seen.cookie,undefined);assert.equal(ok.headers['set-cookie'],undefined);
  await new Promise(r=>upstream.close(r));upstream=null;
  assert.equal((await request()).status,503);
 }finally{await server?.close();if(upstream)await new Promise(r=>upstream.close(r));await rm(dir,{recursive:true,force:true});}
});
