import test from 'node:test';
import assert from 'node:assert/strict';
import {DatabaseSync} from 'node:sqlite';
import {createFixture} from '../fresh-onboarding/fixture.mjs';
import {repairProgress,sqliteRepairRead} from './customer-read.mjs';

test('two native customer logins query only their own SQLite repair records through the real gateway',async t=>{
  const db=new DatabaseSync(':memory:');t.after(()=>db.close());
  db.exec('CREATE TABLE repairs(customer TEXT, ticket TEXT, state TEXT, PRIMARY KEY(customer,ticket))');
  const read=sqliteRepairRead(db);
  let reads=0;
  const lab=await createFixture({customerRead:repairProgress({lookup:(id,ticket)=>{reads++;return read(id,ticket);}})});
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
  const seed=db.prepare('INSERT INTO repairs VALUES(?,?,?)');seed.run(a.id,'R-123','repairing');seed.run(b.id,'R-456','inspecting');
  let n=0;
  async function ask(customer,content,event='evt-'+(++n)){
    const {data}=await call('/chat/v1/messages',{...customer,body:{event_id:event,content}});
    const history=await call('/chat/v1/messages',customer);
    return history.data.messages.find(m=>m.id===data.reply_message_id)?.content;
  }
  assert.match(await ask(a,'我送修的设备修好了吗？'),/请提供维修单号/);
  await ask(b,'R-123');assert.equal(reads,0);
  assert.equal(await ask(a,'R-123','repair-follow'),'这笔维修单正在维修。');
  db.prepare('UPDATE repairs SET state=? WHERE customer=? AND ticket=?').run('ready',a.id,'R-123');
  assert.equal(await ask(a,'R-123','repair-follow'),'这笔维修单正在维修。');
  assert.equal(await ask(a,'维修单号 R-123 修好了吗？'),'这笔维修单已修好，可以领取。');
  assert.equal(await ask(b,'维修单号 R-456 修好了吗？'),'这笔维修单正在检测。');
  assert.equal(await ask(b,'维修单号 R-123 修好了吗？'),'没有找到你可查看的维修单。');
  assert.equal(await ask(b,'维修单号 R-999 修好了吗？'),'没有找到你可查看的维修单。');
  const before=reads;await ask(a,'维修一般要多久？');assert.equal(reads,before);
  db.exec('DROP TABLE repairs');assert.equal(await ask(a,'维修单 R-123 进度'),'暂时查不到维修进度，请稍后再试。');
});
