import test from 'node:test';
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {createFixture} from '../fresh-onboarding/fixture.mjs';
import {repairProgress} from '../device-repair/customer-read.mjs';
import {replyText} from '../host-kit/customer-read.mjs';

function shipping({lookup}){
  return async ({customerID,content,previousExchange})=>{
    const x={customerID,content};
    for(let i=0;i<3;i++){
      const y=JSON.parse(execFileSync('php',[fileURLToPath(new URL('./order-fixture.php',import.meta.url))],{input:JSON.stringify(x),timeout:3000}));
      if(y.result)return y.result;
      if(y.need==='previous')x.previous=await previousExchange();
      else if(y.need==='lookup'){try{x.row=lookup(y.customerID,y.number);}catch{x.row=null;x.failed=true;}}
      else throw Error('unexpected fixture request');
    }
    throw Error('fixture protocol did not terminate');
  };
}
const suites=[
  {name:'PHP order',bind:shipping,ordinary:'你们一般多久发货？',ambiguous:'我的订单发货了吗？',follow:'订单号：R-123',direct:n=>'订单号'+n+'发货了吗？',row:status=>({paid:1,shipping_type:1,is_cancel:0,refund_status:0,status}),answers:['这笔订单尚未发货。','这笔订单已发货，当前待收货。']},
  {name:'Node repair',bind:repairProgress,ordinary:'维修一般要多久？',ambiguous:'我送修的设备修好了吗？',follow:'R-123',direct:n=>'维修单号 '+n+' 修好了吗？',row:i=>({state:i?'ready':'repairing'}),answers:['这笔维修单正在维修。','这笔维修单已修好，可以领取。']}
];
for(const s of suites)test(s.name+' / customer read contract v1',async t=>{
  let reads=0,previousReads=0,broken=false;const rows=new Map(),outcomes=[];
  const handle=s.bind({lookup:(id,n)=>{reads++;if(broken)throw Error('private diagnostic');return rows.get(JSON.stringify([id,n]))??null;}});
  const lab=await createFixture({customerRead:async input=>{
    const result=await handle({...input,previousExchange:async()=>{previousReads++;return input.previousExchange();}});
    outcomes.push(result.kind);return result;
  }});
  t.after(()=>lab.close());await lab.attach();
  async function call(path,{body,cookie,token,status=200}={}){
    const r=await fetch(lab.origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:lab.origin,'Content-Type':'application/json',...(cookie?{Cookie:cookie}:{}),...(token?{Authorization:'Bearer '+token}:{})},body:JSON.stringify(body)});
    assert.equal(r.status,status,path);return {data:await r.json(),cookie:r.headers.get('set-cookie')?.split(';')[0]};
  }
  async function customer(account){
    const {cookie}=await call('/auth/customer/login',{body:{account,password:lab.accounts.find(a=>a.handle===account).passphrase}});
    const {data}=await call('/host/chat-session',{body:{},cookie});
    return {cookie,token:data.access_token,id:data.user.id};
  }
  const a=await customer('lin'),b=await customer('zhou');

  let n=0;
  async function ask(customer,content,event='evt-'+(++n)){
    const {data}=await call('/chat/v1/messages',{...customer,body:{event_id:event,content}});
    const history=await call('/chat/v1/messages',customer);
    return history.data.messages.find(m=>m.id===data.reply_message_id)?.content;
  }

  const key=JSON.stringify([a.id,'R-123']);rows.set(key,s.row(0));
  await ask(a,s.ordinary);assert.deepEqual(outcomes,['pass']);assert.equal(reads+previousReads,0);
  await ask(a,s.ambiguous);assert.equal(outcomes.at(-1),'clarify');assert.equal(reads+previousReads,0);
  await ask(b,s.follow);assert.equal(outcomes.at(-1),'pass');assert.equal(reads,0);
  assert.equal(await ask(a,s.follow,'original'),s.answers[0]);assert.equal(outcomes.at(-1),'answer');
  rows.set(key,s.row(1));
  assert.equal(await ask(a,s.follow,'original'),s.answers[0]);
  assert.equal(await ask(a,s.direct('R-123')),s.answers[1]);
  const absent=await ask(b,s.direct('R-999'));assert.match(absent,/没有找到/);
  assert.equal(await ask(b,s.direct('R-123')),absent);
  broken=true;assert.match(await ask(a,s.direct('R-123')),/暂时查不到/);assert.equal(outcomes.at(-1),'unavailable');
});
test('invalid decisions never become replies',()=>{
 for(const value of [{kind:'other'},{kind:'pass',text:'unexpected'},{kind:'answer',text:' '},{kind:'answer',text:'界'.repeat(342)},{kind:'answer',text:'ok',row:{}}])assert.throws(()=>replyText(value));
});
