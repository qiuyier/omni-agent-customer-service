// Disposable real HTTP/Go/PostgreSQL lab. No CRMEB, model credentials or fixed ports.
import {execFile, spawn} from 'node:child_process';
import {promisify} from 'node:util';
import {mkdtemp, readFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {randomBytes, createHash} from 'node:crypto';
import {Readable, Writable} from 'node:stream';
import {startBusiness} from '../business-login/business.mjs';
import {acceptRestore} from './core.mjs';
import {createBusiness} from './business.mjs';
import {main as cli} from './cli.mjs';

export async function runBusinessCheck() {
  const root=fileURLToPath(new URL('../../',import.meta.url)), run=promisify(execFile);
  const nonce=randomBytes(6).toString('hex'), name='omni-business-acceptance-'+nonce;
  const secret=randomBytes(32).toString('base64url'), agentSecret=randomBytes(32).toString('base64url');
  const password=randomBytes(24).toString('hex');
  const report={scope:'business_password_real_http',authentication:'password_scrypt_cookie',database_restored:false,
    external_model_calls:0,passed:false,runs:[],cleanup:{},support_may_open:false};
  const hash=async()=>createHash('sha256').update(await readFile(new URL('./core.mjs',import.meta.url))).digest('hex');
  report.core_sha256_before=await hash();
  let directory, owned=false, child, closed, host;
  let now=Date.now();
  const passwords={lin:randomBytes(24).toString('hex'),zhou:randomBytes(24).toString('hex'),qiao:randomBytes(24).toString('hex')};
  const docker=(...args)=>run('docker',args,{env:{...process.env,POSTGRES_PASSWORD:password},timeout:60000,maxBuffer:1048576});
  try {
    directory=await mkdtemp(join(tmpdir(),'omni-password-host-'));
    await docker('create','--name',name,'-e','POSTGRES_PASSWORD','-e','POSTGRES_USER=acceptance','-e','POSTGRES_DB=acceptance','-p','127.0.0.1::5432','postgres:16-alpine');owned=true;
    await docker('start',name);
    let ready=false;
    for(let i=0;i<30;i++){try{await docker('exec',name,'pg_isready','-U','acceptance','-d','acceptance');ready=true;break;}catch{await new Promise(r=>setTimeout(r,1000));}}
    if(!ready)throw Error('database_not_ready');
    const binding=(await docker('port',name,'5432/tcp')).stdout.trim();
    if(!/^127\.0\.0\.1:\d+$/.test(binding))throw Error('unexpected_binding');
    const dsn=`postgres://acceptance:${password}@${binding}/acceptance?sslmode=disable`;
    for(const binary of ['migrate','webchat-server'])await run('go',['build','-o',join(directory,binary),'./cmd/'+binary],{cwd:root,timeout:120000});
    const env={PATH:process.env.PATH,DATABASE_URL:dsn,WEBCHAT_HOST_ID:'standalone-'+nonce,WEBCHAT_HOST_SECRET:secret,WEBCHAT_REPLY_MODE:'fake',
      WEBCHAT_AGENTS_JSON:JSON.stringify([{source_id:'standalone-'+nonce,id:'qiao',secret:agentSecret}])};
    await run(join(directory,'migrate'),[],{cwd:root,env,timeout:30000});
    child=spawn(join(directory,'webchat-server'),['-address','127.0.0.1:0'],{cwd:root,env,stdio:['ignore','pipe','pipe']});
    closed=new Promise(r=>child.once('close',r));child.stderr.resume();
    const gatewayOrigin=await new Promise((resolve,reject)=>{
      const timer=setTimeout(()=>reject(Error('gateway_timeout')),20000);let text='';
      child.once('error',()=>{clearTimeout(timer);reject(Error('gateway_start'));});
      child.once('exit',()=>{clearTimeout(timer);reject(Error('gateway_exit'));});
      child.stdout.on('data',x=>{text+=x;const m=text.match(/Web chat server: (http:\/\/127\.0\.0\.1:\d+)/);if(m){clearTimeout(timer);resolve(m[1]);}});
    });
    host=await startBusiness({gatewayOrigin,hostSecret:secret,storage:'postgres',replyMode:'fake',now:()=>now,agents:[{id:'qiao',name:'Test staff',secret:agentSecret}],accounts:[{role:'customer',account:'lin',id:'member-a',password:passwords.lin},{role:'customer',account:'zhou',id:'member-b',password:passwords.zhou},{role:'staff',account:'qiao',id:'qiao',password:passwords.qiao}]});
    const raw=async(path,{cookie,body,capability,method='POST'}={})=>{
      const r=await fetch(host.origin+path,{method,headers:{Origin:host.origin,'Content-Type':'application/json',...(cookie?{Cookie:cookie}:{}),...(capability?{Authorization:'Bearer '+capability}:{})},body:method==='GET'?undefined:JSON.stringify(body||{}),redirect:'error',signal:AbortSignal.timeout(10000)});
      const text=await r.text();return {status:r.status,data:text?JSON.parse(text):null,cookie:r.headers.get('set-cookie')?.split(';')[0]};
    };
    const c={adapter:'business-password',environment:'isolated-business-lab',origin:host.origin,timeout_ms:10000,
      accounts:{a:{account:'lin',password:passwords.lin},b:{account:'zhou',password:passwords.zhou},staff:{account:'qiao',password:passwords.qiao}},old_cookies:{}};
    report.baseline=[];
    for(const who of ['a','b','staff']){
      const login=await raw(who==='staff'?'/auth/staff/login':'/auth/customer/login',{body:c.accounts[who]});
      if(login.status!==200||!login.cookie)throw Error('baseline_login');c.old_cookies[who]=login.cookie;
      const check=await raw(who==='staff'?'/desk/queue':'/host/chat-session',{cookie:login.cookie,method:who==='staff'?'GET':'POST'});
      report.baseline.push({identity:who,passed:check.status===200});if(check.status!==200)throw Error('baseline_invalid');
    }
    const execute=async()=>{
      let text='';const output=new Writable({write(chunk,_,done){text+=chunk;done();}});
      const exit_code=await cli(['--run','--confirm-isolated-copy','--confirm-public-entry-offline'],Readable.from([JSON.stringify(c)]),output);
      return {exit_code,report:JSON.parse(text)};
    };
    report.expected_failure=await execute();
    if(report.expected_failure.exit_code!==1||report.expected_failure.report.failed_stage!=='old_a_denied')throw Error('fault_not_detected');
    for(const who of ['a','b','staff']){
      const r=await raw(who==='staff'?'/auth/staff/logout':'/auth/customer/logout',{cookie:c.old_cookies[who]});if(r.status!==204)throw Error('baseline_logout');
    }
    for(let i=0;i<2;i++) {const r=await execute();report.runs.push(r);if(r.exit_code!==0)throw Error('flow_failed');}
    // Fail after an actual accepted send, then require cleanup to revoke all new logins.
    const fault=createBusiness(c);fault.receipt=async()=>{throw Error('injected_private_failure');};
    report.midflow_failure=await acceptRestore(fault);
    if(report.midflow_failure.passed||report.midflow_failure.failed_stage!=='receipt'||!report.midflow_failure.cleanup_passed)throw Error('midflow_fault_not_detected');

    report.identity_checks=[];
    const ensure=(name,ok)=>{report.identity_checks.push({name,passed:!!ok});if(!ok)throw Error('identity_check_failed');};
    ensure('wrong_password',(await raw('/auth/customer/login',{body:{account:'lin',password:'wrong'}})).status===401);
    ensure('demo_login_absent',(await raw('/demo/login',{body:{account:'lin'}})).status===404);
    for(const role of ['customer','staff']){
      const account=role==='customer'?'lin':'qiao',path=role==='customer'?'/host/chat-session':'/desk/queue';
      const login=()=>raw('/auth/'+role+'/login',{body:{account,password:passwords[account]}});
      const probe=cookie=>raw(path,{cookie,method:role==='customer'?'POST':'GET'});
      const old=await login();ensure(role+'_valid',(await probe(old.cookie)).status===200);
      let capability;
      if(role==='customer'){
        capability=(await raw('/host/chat-session',{cookie:old.cookie})).data.access_token;
        ensure('forged_identity_authenticated',(await raw('/host/chat-session',{cookie:old.cookie,body:{customer_id:'member-b'}})).status===400);
      }
      host.setEnabled(role,account,false);
      ensure(role+'_disabled_denied',(await probe(old.cookie)).status===401);
      if(role==='customer'){
        ensure('disabled_history_denied',(await raw('/chat/v1/messages',{cookie:old.cookie,capability,method:'GET'})).status===401);
        ensure('disabled_send_denied',(await raw('/chat/v1/messages',{cookie:old.cookie,capability,body:{event_id:'disabled-test',content:'must reject'}})).status===401);
      }
      ensure(role+'_disabled_login_denied',(await login()).status===401);
      host.setEnabled(role,account,true);
      ensure(role+'_reenable_old_denied',(await probe(old.cookie)).status===401);
      const fresh=await login();ensure(role+'_fresh_after_enable',(await probe(fresh.cookie)).status===200);
      ensure(role+'_logout',(await raw('/auth/'+role+'/logout',{cookie:fresh.cookie})).status===204);
      ensure(role+'_after_logout_denied',(await probe(fresh.cookie)).status===401);
    }
    const ca=await raw('/auth/customer/login',{body:c.accounts.a}),st=await raw('/auth/staff/login',{body:c.accounts.staff});
    const cap=await raw('/host/chat-session',{cookie:ca.cookie});ensure('expiry_baseline',cap.status===200&&(await raw('/desk/queue',{cookie:st.cookie,method:'GET'})).status===200);
    now+=1800001;
    ensure('expired_customer_denied',(await raw('/host/chat-session',{cookie:ca.cookie})).status===401);
    ensure('expired_history_denied',(await raw('/chat/v1/messages',{cookie:ca.cookie,capability:cap.data.access_token,method:'GET'})).status===401);
    ensure('expired_staff_denied',(await raw('/desk/queue',{cookie:st.cookie,method:'GET'})).status===401);
    report.core_sha256_after=await hash();
    if(report.core_sha256_before!==report.core_sha256_after)throw Error('core_changed');
    report.passed=true;
  } catch {report.passed=false;report.error='business_password_check_failed';}
  finally {
    try{await host?.close();report.cleanup.host_closed=true;}catch{report.cleanup.host_closed=false;}
    try{
      if(child){if(child.exitCode===null&&child.signalCode===null){child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),7000);try{await closed;}finally{clearTimeout(timer);}}else await closed;}
      report.cleanup.gateway_stopped=true;
    }catch{report.cleanup.gateway_stopped=false;}
    try{if(owned)await docker('rm','-f','-v',name);report.cleanup.owned_database_removed=true;}catch{report.cleanup.owned_database_removed=false;}
    try{if(directory)await rm(directory,{recursive:true,force:true});report.cleanup.temporary_binaries_removed=true;}catch{report.cleanup.temporary_binaries_removed=false;}
    if(Object.values(report.cleanup).some(v=>!v))report.passed=false;
  }
  return report;
}
if(process.argv[1]&&resolve(process.argv[1])===fileURLToPath(import.meta.url)){
  const args=process.argv.slice(2);
  if(args.length!==3||args[0]!=='--allow-isolated-test-writes'||args[1]!=='--report'){
    console.error('Requires --allow-isolated-test-writes --report NEW_REPORT_PATH');process.exitCode=1;
  }else{
    // Reserve output before creating resources; do not overwrite a previous report.
    const {open}=await import('node:fs/promises');const output=await open(args[2],'wx',0o600);
    try{const report=await runBusinessCheck();await output.writeFile(JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));process.exitCode=report.passed?0:1;}finally{await output.close();}
  }
}
