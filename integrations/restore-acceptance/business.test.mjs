import test from 'node:test';
import assert from 'node:assert/strict';
import {Readable, Writable} from 'node:stream';
import {createBusiness, validateBusiness} from './business.mjs';
import {main} from './cli.mjs';

const config = () => ({adapter:'business-password',environment:'isolated-business-lab',origin:'http://127.0.0.1:12345',timeout_ms:100,
  accounts:{a:{account:'lin',password:'private-password-a'},b:{account:'zhou',password:'private-password-b'},staff:{account:'qiao',password:'private-password-s'}},
  old_cookies:{a:'repair_customer='+'a'.repeat(43),b:'repair_customer='+'b'.repeat(43),staff:'repair_staff='+'c'.repeat(43)}});
test('strict loopback and separate cookie realms',()=>{
  for(const origin of ['http://localhost:12345','http://127.0.0.1:12345/','https://example.com','http://user@127.0.0.1:12345','http://127.0.0.1:12345?x'])assert.throws(()=>validateBusiness({...config(),origin},true));
  const c=config();c.old_cookies.staff=c.old_cookies.a;assert.throws(()=>validateBusiness(c,true));
});
test('staff rejection probes protected queue, never public desk/me',async()=>{
  const paths=[];const a=createBusiness(config(),{fetchImpl:async(url)=>{paths.push(new URL(url).pathname);return new Response('{}',{status:401});}});
  assert.equal(await a.oldDenied('staff'),true);assert.deepEqual(paths,['/desk/queue']);
});
test('cookie authentication, 204 logout and verified revocation for all roles',async()=>{
  const calls=[], revoked=new Set();
  const a=createBusiness(config(),{fetchImpl:async(url,o)=>{
    const p=new URL(url).pathname;calls.push({p,o});assert.equal(o.redirect,'error');
    if(p.endsWith('/login')){
      const {account,password}=JSON.parse(o.body);assert.match(password,/^private-password-/);const name=p==='/auth/staff/login'?'repair_staff':'repair_customer';
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
  const a=createBusiness(config(),{fetchImpl:async(url)=>{
    if(url.endsWith('/login'))return new Response('{}',{headers:{'set-cookie':(url.endsWith('/auth/staff/login')?'repair_staff':'repair_customer')+'='+'x'.repeat(43)}});
    if(url.endsWith('/logout')){logouts++;return new Response('{}',{status:503});}
    return new Response('{}',{status:401});
  }});
  for(const who of ['a','b','staff'])await a.login(who);
  await assert.rejects(a.cleanup());assert.equal(logouts,3);
});
test('metadata rejects AI and memory before sending messages',async()=>{
  for(const metadata of [{demo:false,reply_mode:'ai',storage:'postgres',human_enabled:true},{demo:false,reply_mode:'fake',storage:'memory',human_enabled:true}]){
    const a=createBusiness(config(),{fetchImpl:async()=>new Response(JSON.stringify({...metadata,access_token:'chat_test'}))});
    await assert.rejects(a.session('a'));
  }
});
test('CLI dispatches business password offline plan, refuses unknown adapters',async()=>{
  for(const [adapter,expected]of [['business-password',0],['arbitrary-module',1]]){
    let raw='';const out=new Writable({write(c,_,done){raw+=c;done();}});
    assert.equal(await main([],Readable.from([JSON.stringify({...config(),adapter})]),out),expected);
    assert.equal(JSON.parse(raw).support_may_open,false);assert.ok(!raw.includes('repair_customer='));
  }
});

test('password adapter rejects missing passwords and demo sessions',async()=>{
 const c=config();delete c.accounts.a.password;assert.throws(()=>validateBusiness(c,true));
 const a=createBusiness(config(),{fetchImpl:async()=>new Response(JSON.stringify({demo:true,reply_mode:'fake',storage:'postgres',human_enabled:true,access_token:'chat_test'}))});
 await assert.rejects(a.session('a'));
});
