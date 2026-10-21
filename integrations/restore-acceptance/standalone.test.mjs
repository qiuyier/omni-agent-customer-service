import test from 'node:test';
import assert from 'node:assert/strict';
import {Readable, Writable} from 'node:stream';
import {createStandalone, validateStandalone} from './standalone.mjs';
import {main} from './cli.mjs';

const config = () => ({adapter:'standalone-demo',environment:'isolated-standalone-lab',origin:'http://127.0.0.1:12345',timeout_ms:100,
  accounts:{a:{account:'lin'},b:{account:'zhou'},staff:{account:'qiao'}},
  old_cookies:{a:'standalone_login='+'a'.repeat(43),b:'standalone_login='+'b'.repeat(43),staff:'standalone_desk='+'c'.repeat(43)}});
test('strict loopback and separate cookie realms',()=>{
  for(const origin of ['http://localhost:12345','http://127.0.0.1:12345/','https://example.com','http://user@127.0.0.1:12345','http://127.0.0.1:12345?x'])assert.throws(()=>validateStandalone({...config(),origin},true));
  const c=config();c.old_cookies.staff=c.old_cookies.a;assert.throws(()=>validateStandalone(c,true));
});
test('staff rejection probes protected queue, never public desk/me',async()=>{
  const paths=[];const a=createStandalone(config(),{fetchImpl:async(url)=>{paths.push(new URL(url).pathname);return new Response('{}',{status:401});}});
  assert.equal(await a.oldDenied('staff'),true);assert.deepEqual(paths,['/desk/queue']);
});
test('cookie authentication, 204 logout and verified revocation for all roles',async()=>{
  const calls=[], revoked=new Set();
  const a=createStandalone(config(),{fetchImpl:async(url,o)=>{
    const p=new URL(url).pathname;calls.push({p,o});assert.equal(o.redirect,'error');
    if(p.endsWith('/login')){
      const account=JSON.parse(o.body).account;const name=p==='/desk/login'?'standalone_desk':'standalone_login';
      return new Response('{}',{headers:{'set-cookie':name+'='+account[0].repeat(43)+'; HttpOnly; SameSite=Strict'}});
    }
    if(p.endsWith('/logout')){revoked.add(o.headers.Cookie);return new Response(null,{status:204});}
    return new Response('{}',{status:revoked.has(o.headers.Cookie)?401:200});
  }});
  for(const who of ['a','b','staff'])await a.login(who);
  await a.cleanup();assert.equal(revoked.size,3);
  assert.ok(calls.every(x=>!x.o.headers['Authori-zation']));
});
test('cleanup attempts other logins after one logout fails',async()=>{
  let logouts=0;
  const a=createStandalone(config(),{fetchImpl:async(url)=>{
    if(url.endsWith('/login'))return new Response('{}',{headers:{'set-cookie':(url.endsWith('/desk/login')?'standalone_desk':'standalone_login')+'='+'x'.repeat(43)}});
    if(url.endsWith('/logout')){logouts++;return new Response('{}',{status:503});}
    return new Response('{}',{status:401});
  }});
  for(const who of ['a','b','staff'])await a.login(who);
  await assert.rejects(a.cleanup());assert.equal(logouts,3);
});
test('metadata rejects AI and memory before sending messages',async()=>{
  for(const metadata of [{demo:true,reply_mode:'ai',storage:'postgres',human_enabled:true},{demo:true,reply_mode:'fake',storage:'memory',human_enabled:true}]){
    const a=createStandalone(config(),{fetchImpl:async()=>new Response(JSON.stringify({...metadata,access_token:'chat_test'}))});
    await assert.rejects(a.session('a'));
  }
});
test('CLI dispatches standalone offline plan, refuses unknown adapters',async()=>{
  for(const [adapter,expected]of [['standalone-demo',0],['arbitrary-module',1]]){
    let raw='';const out=new Writable({write(c,_,done){raw+=c;done();}});
    assert.equal(await main([],Readable.from([JSON.stringify({...config(),adapter})]),out),expected);
    assert.equal(JSON.parse(raw).support_may_open,false);assert.ok(!raw.includes('standalone_login='));
  }
});
