import test from 'node:test';
import assert from 'node:assert/strict';
import {Readable, Writable} from 'node:stream';
import {acceptRestore} from './core.mjs';
import {createCRMEB, validateConfig} from './crmeb.mjs';
import {main} from './cli.mjs';

const config = () => ({adapter: 'crmeb', environment: 'isolated-restored-copy', origin: 'http://127.0.0.1:12345', timeout_ms: 100,
  accounts: Object.fromEntries(['a','b','staff'].map(w => [w, {account: w, password: 'PRIVATE_PASSWORD'}])),
  old_tokens: Object.fromEntries(['a','b','staff'].map(w => [w, 'old_PRIVATE_TOKEN_' + w.padEnd(8, 'x')]))});
function fixture(fault = '') {
  const c = config(), calls = [], states = {a: {id:'ca0',status:'bot_active',messages:[],revision:1}, b:{id:'cb',status:'bot_active',messages:[],revision:1}};
  let generation = 0;
  const reply = (data, status=200) => new Response(JSON.stringify(data), {status});
  const fetchImpl = async (url, opts) => {
    const path = new URL(url).pathname, body = opts.body ? JSON.parse(opts.body) : {}, token = opts.headers['Authori-zation']?.slice(7);
    calls.push({path, body, token, opts});
    assert.equal(opts.redirect, 'error'); assert.equal(opts.headers.Origin, c.origin);
    if (token?.startsWith('old_')) return reply({}, fault==='old_accepted'?200:401);
    if (path.endsWith('/login')) {
      if(fault==='partial_login' && body.account==='b') throw Error('PRIVATE_PASSWORD PRIVATE_TOKEN');
      return reply({status:200,data:{token:'new_'+body.account}});
    }
    if (path.endsWith('/logout')) return reply({status:fault==='cleanup'?500:200});
    const who = token?.slice(4), state = states[who];
    if(path.endsWith('/desk/me')) return reply({agent:{id:'agent'}});
    if(path.endsWith('/session')) return reply({conversation_id:state.id,access_token:'cap_'+who,reply_mode:fault==='ai'?'ai':'fake',storage:'postgres'});
    if(path.endsWith('/messages') && opts.headers['X-Omni-Chat-Token']!=='cap_'+who) return reply({},401);
    if(path.endsWith('/consultations')) {generation++; states.a={id:'ca'+generation,status:'bot_active',messages:[],revision:1};return reply({});}
    if(path.endsWith('/messages') && opts.method==='GET') {
      if(fault==='history_throw') throw Error('PRIVATE_TOKEN');
      const messages = fault==='leak' && who==='b'?states.a.messages:state.messages;
      return reply({conversation_id:state.id,status:state.status,messages});
    }
    if(path.endsWith('/messages')) {
      const id=body.event_id;
      if(!state.messages.some(m=>m.id===id)||fault==='duplicate')state.messages.push({id,content:body.content});
      return reply({conversation_id:state.id,message_id:id},201);
    }
    if(path.endsWith('/message-receipt'))return reply({accepted:true,conversation_id:state.id,message_id:fault==='receipt'?'wrong':body.event_id});
    if(path.endsWith('/handoff')) {state.status='waiting_human';return reply({});}
    if(path.endsWith('/desk/action')) {
      const s=states.a;
      if(body.action==='reply' && fault==='reply_failure')throw Error('PRIVATE_REPLY');
      if(body.action==='claim'){s.status='human_active';s.revision++;}
      if(body.action==='reply')s.messages.push({id:body.event_id,content:body.content});
      if(body.action==='close')s.status='closed';
      return reply({status:s.status,revision:s.revision,messages:s.messages});
    }
    throw Error('unexpected_fixture_route');
  };
  return {c,calls,states,adapter:()=>createCRMEB(c,{fetchImpl})};
}

test('complete adapter flow repeats against same restored state with a fresh round',async()=>{
  const f=fixture();
  for(let i=0;i<2;i++){
    const r=await acceptRestore(f.adapter());
    assert.equal(r.passed,true,JSON.stringify(r));assert.equal(r.cleanup_passed,true);assert.equal(r.support_may_open,false);
    assert.equal(r.browser_verified,false);
  }
  assert.equal(f.calls.filter(c=>c.path.endsWith('/logout')).length,6);
  assert.equal(f.calls.filter(c=>c.path.endsWith('/consultations')).length,1);
});
for(const [fault,stage]of [['old_accepted','old_a_denied'],['partial_login','fresh_logins'],['ai','customer_sessions'],['receipt','receipt'],['duplicate','stored_once'],['leak','history_isolated'],['history_throw','history']]){
  test(`fail closed: ${fault}`,async()=>{
    const f=fixture(fault),r=await acceptRestore(f.adapter());
    assert.equal(r.passed,false);assert.equal(r.failed_stage,stage);assert.equal(r.support_may_open,false);assert.equal(r.cleanup_passed,true);
    assert.ok(!JSON.stringify(r).includes('PRIVATE'));
    if(['old_accepted','partial_login','ai'].includes(fault))assert.equal(f.calls.filter(c=>c.path.endsWith('/messages')&&c.opts.method==='POST').length,0);
    if(fault==='partial_login')assert.equal(f.calls.filter(c=>c.path.endsWith('/logout')).length,1);
  });
}
test('cleanup failure fails acceptance and still attempts every logout',async()=>{
  const f=fixture('cleanup'),r=await acceptRestore(f.adapter());assert.equal(r.passed,false);assert.equal(r.cleanup_failed,true);
  assert.equal(f.calls.filter(c=>c.path.endsWith('/logout')).length,3);
});
test('active human consultation is preserved, not silently closed',async()=>{
  const f=fixture();f.states.a.status='human_active';
  const r=await acceptRestore(f.adapter());assert.equal(r.failed_stage,'available_test_conversation');
  assert.equal(f.calls.filter(c=>c.body.action==='close').length,0);
});
test('mid-flow failure cleans logins, retains evidence and blocks unsafe rerun',async()=>{
  const f=fixture('reply_failure');
  const first=await acceptRestore(f.adapter());
  assert.equal(first.passed,false);assert.equal(first.failed_stage,'reply');assert.equal(first.cleanup_passed,true);
  assert.equal(f.states.a.status,'human_active');
  const second=await acceptRestore(f.adapter());
  assert.equal(second.failed_stage,'available_test_conversation');assert.equal(second.support_may_open,false);
  assert.equal(f.calls.filter(c=>c.body.action==='close').length,0);
  assert.equal(f.calls.filter(c=>c.path.endsWith('/logout')).length,6);
});
test('reject remote, credentials, ambiguous origins and duplicate customer accounts',()=>{
  for(const origin of ['https://example.com','http://localhost:1234','http://127.0.0.1:1234/','http://x:y@127.0.0.1:1234','http://127.0.0.1:1234/path','http://127.0.0.1:1234?x','http://127.0.0.1:1234#x'])assert.throws(()=>validateConfig({...config(),origin},true));
  const c=config();c.accounts.b.account=c.accounts.a.account;assert.throws(()=>validateConfig(c,true));
});
test('default CLI is an offline plan, stdout contains no credentials',async()=>{
  let text='';const out=new Writable({write(chunk,_,cb){text+=chunk;cb();}});
  assert.equal(await main([],Readable.from([JSON.stringify(config())]),out),0);
  const r=JSON.parse(text);assert.equal(r.executed,false);assert.equal(r.support_may_open,false);assert.ok(!text.includes('PRIVATE'));
});
test('CLI requires both explicit confirmations and rejects unknown arguments',async()=>{
  for(const args of [['--run'],['--run','--confirm-isolated-copy'],['--open'],['--run','--run']]){
    let text='';const out=new Writable({write(chunk,_,cb){text+=chunk;cb();}});
    assert.equal(await main(args,Readable.from([JSON.stringify(config())]),out),1);assert.equal(JSON.parse(text).support_may_open,false);
  }
});
test('request timeout and oversized response fail without leaking bodies',async()=>{
  for(const kind of ['timeout','oversize']){
    const c=config();const fetchImpl=async(_url,{signal})=>{
      if(kind==='oversize')return new Response('PRIVATE'.repeat(200000));
      return new Promise((_,reject)=>signal.addEventListener('abort',()=>reject(Error('PRIVATE')),{once:true}));
    };
    const r=await acceptRestore(createCRMEB(c,{fetchImpl}));assert.equal(r.passed,false);assert.ok(!JSON.stringify(r).includes('PRIVATE'));
  }
});
test('generic runner invokes cleanup when preflight fails',async()=>{
  let cleaned=false;
  const r=await acceptRestore({preflight(){throw Error('secret');},cleanup(){cleaned=true;}});
  assert.equal(cleaned,true);assert.equal(r.failed_stage,'preflight');assert.equal(r.support_may_open,false);
});
