import test from 'node:test';
import assert from 'node:assert/strict';
import {verifyHost,baseline} from './verify.mjs';
import {createFixture} from '../fresh-onboarding/fixture.mjs';

test('receipt baseline: configured path, fail-closed mutations and cleanup',{timeout:180000},async t=>{
 const cases=[
  ['valid',null],
  ['missing','receipt_accepted'],
  ['wrong_message','receipt_accepted'],
  ['unknown_leaks_id','receipt_unknown'],
  ['conflict_accepted','receipt_conflict'],
  ['cross_customer_leak','receipt_other_customer_isolated'],
  ['closed_missing','receipt_closed'],
  ['logout_allowed','receipt_logout_denied'],
 ];
 for(const [mode,failedCheck] of cases)await t.test(mode,async t=>{
  const fixture=await createFixture();t.after(()=>fixture.close());await fixture.attach();
  const credentials=handle=>({account:handle,password:fixture.accounts.find(x=>x.handle===handle).passphrase});
  const config={origin:fixture.origin,customers:['lin','zhou'].map(credentials),staff:credentials('staff'),paths:{receipt:'/custom/receipt',customerLogin:'/auth/customer/login',customerLogout:'/auth/customer/logout',staffLogin:'/auth/staff/login',staffLogout:'/auth/staff/logout'}};
  let receipts=0,sends=0,logouts=0;
  const fetcher=async(url,options)=>{
   const path=new URL(url).pathname;
   if(path.endsWith('/logout'))logouts++;
   if(path==='/chat/v1/messages'&&options.method==='POST')sends++;
   if(path!=='/custom/receipt')return fetch(url,options);
   receipts++;
   const response=await fetch(fixture.origin+'/chat/v1/message-receipt',options);
   const data=await response.json();let status=response.status;
   if(mode==='missing'){status=404;}
   if(mode==='wrong_message'&&receipts===1)data.message_id='wrong';
   if(mode==='unknown_leaks_id'&&receipts===2)data.message_id='leaked';
   if(mode==='conflict_accepted'&&receipts===3)status=200;
   if(mode==='cross_customer_leak'&&receipts===5){data.accepted=true;data.message_id='leaked';}
   if(mode==='closed_missing'&&receipts===6)data.accepted=false;
   if(mode==='logout_allowed'&&receipts===8)status=200;
   return Response.json(data,{status});
  };
  const report=await verifyHost(config,{allowTestWrites:true,fetcher});
  assert.equal(report.baseline,baseline);assert.equal(report.passed,failedCheck===null,JSON.stringify(report));
  if(failedCheck)assert.equal(report.failed_check,failedCheck,JSON.stringify(report));
  else {
   assert.equal(receipts,8);assert.equal(sends,3); // send + same-event retry + closed rejection; queries never resend.
   for(const check of ['receipt_read_only','receipt_closed_read_only','receipt_old_binding_rejected'])assert.ok(report.checks.includes(check));
  }
  assert.ok(logouts>=3);assert.equal(report.cleanup_failures,undefined);
  for(const a of fixture.accounts)assert.ok(!JSON.stringify(report).includes(a.passphrase));
 });
});

test('no consent makes no request',async()=>{
 let calls=0;const report=await verifyHost({}, {fetcher:async()=>{calls++;}});
 assert.equal(calls,0);assert.equal(report.passed,false);assert.equal(report.error,'test_write_consent_required');
});
