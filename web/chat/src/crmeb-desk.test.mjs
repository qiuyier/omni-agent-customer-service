import test from 'node:test';
import assert from 'node:assert/strict';
import {readStaffLogin,createCRMEBDeskAPI} from './crmeb-desk.mjs';
const reply=()=>new Response(JSON.stringify({agent:{id:'staff'}}),{headers:{'Content-Type':'application/json'}});
test('staff cookie rejects customer, duplicate and malformed credentials',()=>{
 assert.equal(readStaffLogin('token=a.b.c'),'');
 assert.equal(readStaffLogin('from-crmeb-admin%3Akefu_token=a.b.c'),'a.b.c');
 assert.equal(readStaffLogin('from-crmeb-admin:kefu_token=a.b.c; from-crmeb-admin:kefu_token=d.e.f'),'');
 assert.equal(readStaffLogin('from-crmeb-admin:kefu_token=%XX'),'');
});
test('staff calls pin identity and forward only to adapter',async()=>{
 let login='a.b.c';const calls=[];
 const api=createCRMEBDeskAPI({getLogin:()=>login,fetcher:async(url,init)=>{calls.push({url,init});return reply();}});
 await assert.rejects(api('/desk/queue'),{status:401});
 await api('/desk/me');await api('/desk/queue');
 assert.equal(calls[1].url,'/api/omni-chat/desk/queue');
 assert.equal(calls[1].init.headers['Authori-zation'],'Bearer a.b.c');
 assert.equal(calls[1].init.headers.Authorization,undefined);
 assert.equal(calls[1].init.redirect,'error');
 login='d.e.f';await assert.rejects(api('/desk/action',{method:'POST',body:{}}),{status:401});
 await assert.rejects(api('/internal/chat/desk/queue'),{status:404});
 assert.equal(calls.length,2);
});
test('late staff response is rejected on logout',async()=>{
 let login='a.b.c';const api=createCRMEBDeskAPI({getLogin:()=>login,fetcher:async()=>{login='';return reply();}});
 await assert.rejects(api('/desk/me'),{status:401});
});
test('late failure cannot hide an identity switch',async()=>{
 let login='a.b.c';const api=createCRMEBDeskAPI({getLogin:()=>login,fetcher:async()=>{login='d.e.f';throw new Error('offline');}});
 await assert.rejects(api('/desk/me'),{status:401});
});
