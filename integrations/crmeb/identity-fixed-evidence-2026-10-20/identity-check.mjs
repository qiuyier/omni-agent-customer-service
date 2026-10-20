import {createInterface} from 'node:readline';
import {execFileSync} from 'node:child_process';
import {randomUUID} from 'node:crypto';
import {writeFileSync} from 'node:fs';
const rl=createInterface({input:process.stdin});
const credentials=await new Promise(r=>rl.once('line',s=>{rl.close();r(JSON.parse(s));}));
const origin='http://demo.crmeb.com',checks=[],observations={},tokens=[];
const helper=String.raw`
require '/www/CRMEB/crmeb/vendor/autoload.php';
$app=new \think\App('/www/CRMEB/crmeb/');$app->initialize();
$in=json_decode(stream_get_contents(STDIN),true);
$role=$in['role'];if(!in_array($role,['customer','staff'],true))exit(1);
$model=$role==='customer'?\app\model\user\User::where('account|phone',$in['account']):\app\model\service\StoreService::where('account',$in['account']);
$row=$model->find();if(!$row)exit(2);$id=(int)$row[$role==='customer'?'uid':'id'];
if($in['action']==='read'){echo json_encode(['id'=>$id,'status'=>(int)$row['status']]);}
elseif($in['action']==='status'){if($id!==$in['id']||!in_array($in['status'],[0,1],true))exit(3);$row->status=$in['status'];$row->save();echo json_encode(['status'=>(int)$row['status']]);}
elseif($in['action']==='short'){if($id!==$in['id']||(int)$row['status']!==1)exit(4);
$now=time();$r=app()->make(\crmeb\utils\JwtAuth::class)->createToken($id,$role==='customer'?'api':'kefu',['iat'=>$now-120,'nbf'=>$now-120,'exp'=>$now-48,'test_nonce'=>bin2hex(random_bytes(8))]);echo json_encode(['token'=>$r['token']]);}
elseif($in['action']==='native'){try{if($role==='customer')app()->make(\app\services\user\UserAuthServices::class)->parseToken($in['token']);else app()->make(\app\services\kefu\LoginServices::class)->parseToken($in['token']);echo json_encode(['accepted'=>true]);}catch(\Throwable $e){echo json_encode(['accepted'=>false]);}}
elseif($in['action']==='atomic'){
 $realm=$role==='customer'?'api':'kefu';$read=static function()use($realm,$id){return \think\facade\Db::name('omni_identity_revision')->where(['realm'=>$realm,'subject_id'=>$id])->value('revision');};
 $original=$read();\think\facade\Db::startTrans();
 try{$row->status=0;$row->save();$disabled=$read();$row->status=1;$row->save();$restored=$read();}finally{\think\facade\Db::rollback();}
 echo json_encode(['rotated'=>$original!==$disabled&&$disabled!==$restored&&$original!==$restored,'rollback_restores_revision'=>$read()===$original]);}
else exit(5);`;
const php=(role,action,extra={})=>JSON.parse(execFileSync('docker',['--context','desktop-linux','exec','-i','php74','php','-r',helper],{input:JSON.stringify({role,action,account:role==='customer'?credentials.users[0].account:credentials.staff.account,...extra}),encoding:'utf8',stdio:['pipe','pipe','pipe'],timeout:15000}));
const ensure=(ok,name)=>{if(!ok)throw Error(name);checks.push(name);};
async function call(path,token='',body,cap=''){
 const r=await fetch(origin+path,{method:body===undefined?'GET':'POST',headers:{Origin:origin,...(token?{'Authori-zation':'Bearer '+token}:{}),...(cap?{'X-Omni-Chat-Token':cap}:{}),...(body===undefined?{}:{'Content-Type':'application/json'})},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(12000),redirect:'error'});
 return {status:r.status,data:await r.json()};
}
async function login(role){
 const r=await call(role==='customer'?'/api/login':'/kefuapi/login','',role==='customer'?credentials.users[0]:credentials.staff);
 ensure(!!r.data?.data?.token,role+'_login');const t=r.data.data.token;tokens.push({role,token:t});return t;
}
const state={},restoreFailures=[];let report;
try{
 for(const role of ['customer','staff']){state[role]=php(role,'read');ensure(state[role].status===1,role+'_initially_enabled');}
 let customer=await login('customer'),staff=await login('staff');
 const session=await call('/api/omni-chat/session',customer,{});ensure(session.status===200&&session.data.reply_mode==='fake','fake_required');
 const cap=session.data.access_token,conversation=session.data.conversation_id;
 const before=await call('/api/omni-chat/messages',customer,undefined,cap);ensure(before.status===200,'initial_history');
 const me=await call('/api/omni-chat/desk/me',staff);ensure(me.status===200,'staff_mapped');
 for(const role of ['customer','staff']){
  const atomic=php(role,'atomic');
  ensure(atomic.rotated&&atomic.rollback_restores_revision,role+'_transactional_rotation_and_rollback');
  await new Promise(r=>setTimeout(r,1100));
  const dormant=await login(role);
  ensure(dormant!==(role==='customer'?customer:staff),role+'_distinct_unaccessed_login');
  ensure(php(role,'status',{id:state[role].id,status:0}).status===0,role+'_disabled');
  try{
   const auth=role==='customer'?customer:staff;
   const denied=role==='customer'
    ? [await call('/api/omni-chat/messages',auth,undefined,cap),await call('/api/omni-chat/session',auth,{}),await call('/api/omni-chat/message-receipt',auth,{event_id:randomUUID(),content:'identity test'},cap),await call('/api/omni-chat/messages',auth,{event_id:randomUUID(),content:'must be denied'},cap)]
    : [await call('/api/omni-chat/desk/me',auth),await call('/api/omni-chat/desk/queue',auth),await call('/api/omni-chat/desk/action',auth,{action:'history',conversation_id:conversation,expected_agent_id:me.data.agent.id}),await call('/api/omni-chat/desk/action',auth,{action:'reply',conversation_id:conversation,expected_agent_id:me.data.agent.id,event_id:randomUUID(),content:'must be denied'})];
   ensure(denied.every(x=>x.status===401),role+'_disabled_reads_and_writes_denied');
   const blocked=await call(role==='customer'?'/api/login':'/kefuapi/login','',role==='customer'?credentials.users[0]:credentials.staff);
   ensure(!blocked.data?.data?.token,role+'_disabled_login_denied');
  }finally{ensure(php(role,'status',{id:state[role].id,status:state[role].status}).status===state[role].status,role+'_restored');}
  const old=await call(role==='customer'?'/api/omni-chat/session':'/api/omni-chat/desk/me',role==='customer'?customer:staff,role==='customer'?{}:undefined);
  observations[role+'_old_login_after_restore']=old.status;
  const untouched=await call(role==='customer'?'/api/omni-chat/session':'/api/omni-chat/desk/me',dormant,role==='customer'?{}:undefined);
  observations[role+'_untouched_login_after_restore']=untouched.status;
  ensure(old.status===401&&untouched.status===401,role+'_both_old_logins_revoked');
  ensure(php(role,'native',{token:dormant}).accepted,role+'_native_login_semantics_preserved');
  const fresh=await login(role);
  const renewed=await call(role==='customer'?'/api/omni-chat/session':'/api/omni-chat/desk/me',fresh,role==='customer'?{}:undefined);
  ensure(renewed.status===200,role+'_fresh_login_works');
  if(role==='customer'){
   ensure(renewed.data.conversation_id===conversation,'relogin_same_conversation');
   const history=await call('/api/omni-chat/messages',fresh,undefined,renewed.data.access_token);
   ensure(JSON.stringify(history.data.messages)===JSON.stringify(before.data.messages),'relogin_history_unchanged');
   customer=fresh;
  }else staff=fresh;
 }
 const shorts={};
 for(const role of ['customer','staff']){
  shorts[role]=php(role,'short',{id:state[role].id}).token;tokens.push({role,token:shorts[role]});
  const r=await call(role==='customer'?'/api/omni-chat/session':'/api/omni-chat/desk/me',shorts[role],role==='customer'?{}:undefined);
  ensure(r.status===200,role+'_short_token_initially_valid');
 }
 await new Promise(r=>setTimeout(r,14000));
 for(const role of ['customer','staff']){
  const r=await call(role==='customer'?'/api/omni-chat/session':'/api/omni-chat/desk/me',shorts[role],role==='customer'?{}:undefined);
  ensure(r.status===401,role+'_short_token_expired_denied');
 }
 report={completed:true,passed:observations.customer_old_login_after_restore===401&&observations.staff_old_login_after_restore===401};
}catch(e){report={completed:false,passed:false,failed_check:/^[a-z0-9_]+$/.test(e.message)?e.message:'transport_or_helper_failure'};}
finally{
 for(const role of Object.keys(state))try{php(role,'status',{id:state[role].id,status:state[role].status});}catch{restoreFailures.push(role);}
 let cleanupFailures=0;
 for(const {role,token} of tokens)try{const r=await call(role==='customer'?'/api/logout':'/kefuapi/user/logout',token,role==='customer'?undefined:{});if(![200,401,402].includes(r.status))cleanupFailures++;}catch{cleanupFailures++;}
 report={...report,checked_at:new Date().toISOString(),checks,observations,restore_failures:restoreFailures,cleanup_failures:cleanupFailures,external_model_calls:0,expiry_scope:'server-signed short test JWT; 60s leeway respected; no global config changes; not 30-day natural expiry or browser test'};
 if(restoreFailures.length||cleanupFailures)report.passed=false;
 writeFileSync('/tmp/crmeb-identity-fixed-report.json',JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));process.exitCode=report.passed?0:1;
}
