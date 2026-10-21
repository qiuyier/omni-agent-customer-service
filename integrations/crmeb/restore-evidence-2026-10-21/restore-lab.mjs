import {execFileSync} from 'node:child_process';
import {mkdtempSync,writeFileSync,readFileSync,chmodSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {randomBytes,createHash} from 'node:crypto';
const dir=mkdtempSync(join(tmpdir(),'omni-restore-'));chmodSync(dir,0o700);
const suffix=randomBytes(5).toString('hex'),network='omni-restore-'+suffix,mysql=network+'-mysql',pg=network+'-pg';
const database='omni_restore_'+suffix,password=randomBytes(24).toString('hex');
const checks=[],created=[];let stage='preflight',report;
const docker=(args,options={})=>execFileSync('docker',['--context','desktop-linux',...args],{maxBuffer:128*1024*1024,timeout:120000,stdio:['pipe','pipe','pipe'],...options});
const text=(args,options)=>docker(args,options).toString('utf8').trim();
const ensure=(ok,name)=>{if(!ok)throw Error(name);checks.push(name);};
const hash=b=>createHash('sha256').update(b).digest('hex');
const sourceMysql=sql=>docker(['exec','mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B crmeb -e "$1"','sh',sql]);
const targetMysql=sql=>docker(['exec',mysql,'sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B "$1" -e "$2"','sh',database,sql]);
const sourcePg=sql=>docker(['exec','omni-crmeb-webchat-test-postgres-1','psql','-h','127.0.0.1','-p','15434','-U','omni_chat','-d','omni_crmeb_chat','-Atc',sql]);
const targetPg=sql=>docker(['exec',pg,'psql','-U','omni_restore','-d',database,'-Atc',sql]);
const pgTables=['conversations','messages','desk_read_receipts'];
const pgSQL=t=>`COPY (SELECT row_to_json(t) FROM (SELECT * FROM customer_service.${t} ORDER BY ${t==='desk_read_receipts'?'agent_id,message_id':'id'}) t) TO STDOUT`;
const revisionSQL='SELECT realm,subject_id,revision FROM eb_omni_identity_revision ORDER BY realm,subject_id';
const accountSQL="SELECT 'api',uid,status,is_del FROM eb_user UNION ALL SELECT 'kefu',id,status,0 FROM eb_store_service ORDER BY 1,2";
const sequenceSQL="COPY (SELECT schemaname,sequencename,last_value,start_value,increment_by,min_value,max_value,cache_size,cycle FROM pg_sequences ORDER BY schemaname,sequencename) TO STDOUT";
const constraintSQL="COPY (SELECT conrelid::regclass::text,conname,pg_get_constraintdef(oid) FROM pg_constraint WHERE connamespace='customer_service'::regnamespace ORDER BY conrelid::regclass::text,conname) TO STDOUT";
const mysqlChecksums=run=>{
 const tables=run("SHOW FULL TABLES WHERE Table_type='BASE TABLE'").toString().trim().split('\n').filter(Boolean).map(s=>s.split('\t')[0]);
 if(tables.some(t=>!/^[A-Za-z0-9_]+$/.test(t)))throw Error('invalid_table_name');
 const rows=run('CHECKSUM TABLE '+tables.map(t=>'`'+t+'`').join(',')+' EXTENDED').toString().trim().split('\n').map(s=>{const [table,value]=s.split('\t');if(value==='NULL')throw Error('unsupported_checksum');return [table.split('.').at(-1),value];}).sort((a,b)=>a[0].localeCompare(b[0]));
 return {tables:tables.length,sha256:hash(JSON.stringify(rows))};
};
try{
 const sourceBefore={mysql:mysqlChecksums(sourceMysql),sequences:hash(sourcePg(sequenceSQL)),constraints:hash(sourcePg(constraintSQL)),revisions:hash(sourceMysql(revisionSQL)),accounts:hash(sourceMysql(accountSQL)),pg:Object.fromEntries(pgTables.map(t=>[t,hash(sourcePg(pgSQL(t)))]))};
 stage='backup';
 const myDump=docker(['exec','mysql','sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -uroot --single-transaction --routines --events --triggers --set-gtid-purged=OFF --no-tablespaces crmeb']);
 ensure(!/^(?:USE |CREATE DATABASE )/m.test(myDump.toString()),'mysql_dump_has_no_source_database_switch');
 const pgDump=docker(['exec','omni-crmeb-webchat-test-postgres-1','pg_dump','-h','127.0.0.1','-p','15434','-U','omni_chat','-d','omni_crmeb_chat','-Fc']);
 writeFileSync(join(dir,'business.sql'),myDump,{mode:0o600});writeFileSync(join(dir,'chat.dump'),pgDump,{mode:0o600});
 const backups={mysql:{bytes:myDump.length,sha256:hash(myDump)},postgres:{bytes:pgDump.length,sha256:hash(pgDump)}};
 stage='isolated_services';
 text(['network','create','--internal','--label','omni.restore.lab='+suffix,network]);created.push('network');
 text(['run','-d','--name',mysql,'--network',network,'--label','omni.restore.lab='+suffix,'-e','MYSQL_ROOT_PASSWORD','-e','MYSQL_ROOT_HOST=%','-e','MYSQL_DATABASE='+database,'mysql:8.0.34','--event-scheduler=OFF'],{env:{...process.env,MYSQL_ROOT_PASSWORD:password}});created.push(mysql);
 text(['run','-d','--name',pg,'--network',network,'--label','omni.restore.lab='+suffix,'-e','POSTGRES_PASSWORD','-e','POSTGRES_USER=omni_restore','-e','POSTGRES_DB='+database,'postgres:16.15-bookworm'],{env:{...process.env,POSTGRES_PASSWORD:password}});created.push(pg);
 for(let i=0;i<60;i++){
  try{targetMysql('SELECT 1');targetPg('SELECT 1');checks.push('isolated_databases_ready');break;}
  catch{if(i===59)throw Error('isolated_database_startup_timeout');await new Promise(r=>setTimeout(r,1000));}
 }
 stage='restore';
 docker(['exec','-i',mysql,'sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot "$1"','sh',database],{input:myDump});
 docker(['exec','-i',pg,'pg_restore','--exit-on-error','--no-owner','--no-privileges','-U','omni_restore','-d',database],{input:pgDump});
 ensure(hash(targetMysql(revisionSQL))===sourceBefore.revisions,'mysql_restored_versions_exact');
 ensure(hash(targetMysql(accountSQL))===sourceBefore.accounts,'mysql_restored_account_states_exact');
 ensure(JSON.stringify(mysqlChecksums(targetMysql))===JSON.stringify(sourceBefore.mysql),'mysql_all_tables_checksums_exact');
 for(const t of pgTables)ensure(hash(targetPg(pgSQL(t)))===sourceBefore.pg[t],'postgres_restored_exact_'+t);
 ensure(hash(targetPg(sequenceSQL))===sourceBefore.sequences,'postgres_sequence_values_restored');
 ensure(hash(targetPg(constraintSQL))===sourceBefore.constraints,'postgres_constraints_restored');
 ensure(Number(text(['exec',mysql,'sh','-c','MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -e "SELECT @@event_scheduler"']))===0 || targetMysql('SELECT @@event_scheduler').toString().trim()==='OFF','restored_business_events_disabled');
 const net=JSON.parse(text(['network','inspect',network]))[0];ensure(net.Internal===true,'isolated_internal_network');
 for(const name of [mysql,pg]){
  const info=JSON.parse(text(['inspect',name]))[0];ensure(!info.HostConfig.PortBindings||Object.keys(info.HostConfig.PortBindings).length===0,name===mysql?'mysql_no_published_ports':'postgres_no_published_ports');
 }
 stage='identity_restore_guard';
 writeFileSync(join(dir,'settings.json'),JSON.stringify({database,mysql,password,signing_key:randomBytes(32).toString('hex')}),{mode:0o600});
 for(const file of ['IdentityRestoreGuard.php','crmeb-restore-verify.php'])writeFileSync(join(dir,file),readFileSync('/tmp/'+file),{mode:0o600});
 const verifyArgs=['run','--rm','--network',network,'--entrypoint','php','-v',dir+':/lab:ro','-v','/Users/LOCAL_USER/Code/DockerProject/dnmp8/www/CRMEB/crmeb:/www/CRMEB/crmeb:ro','dnmp8-php74:latest','/lab/crmeb-restore-verify.php'];
 let verification;
 try{verification=JSON.parse(text(verifyArgs));}catch(e){
  const out=e.stdout?.toString().trim();if(out)try{verification=JSON.parse(out);}catch{}
  if(verification)writeFileSync(join(dir,'identity-report.json'),JSON.stringify(verification,null,2),{mode:0o600});
  throw Error(verification?.error||'isolated_php_verification_failed');
 }
 writeFileSync(join(dir,'identity-report.json'),JSON.stringify(verification,null,2),{mode:0o600});ensure(verification.passed,'restored_identity_guard_verified');
 stage='source_unchanged';
 ensure(hash(sourceMysql(revisionSQL))===sourceBefore.revisions,'source_versions_unchanged');
 ensure(hash(sourceMysql(accountSQL))===sourceBefore.accounts,'source_account_states_unchanged');
 ensure(JSON.stringify(mysqlChecksums(sourceMysql))===JSON.stringify(sourceBefore.mysql),'source_mysql_all_tables_unchanged');
 for(const t of pgTables)ensure(hash(sourcePg(pgSQL(t)))===sourceBefore.pg[t],'source_unchanged_'+t);
 report={passed:true,checks,backups,mysql_tables_verified:sourceBefore.mysql.tables,identity:verification,scope:'local logical backups restored to independent internal-network databases; no public support endpoint opened',external_model_calls:0};
}catch(e){report={passed:false,checks,failed_stage:stage,error:/^[a-z_]+$/.test(e.message)?e.message:'restore_lab_failed'};}
finally{
 const stopFailures=[];
 for(const name of created.filter(x=>x!=='network'))try{docker(['stop',name]);}catch{stopFailures.push(name);}
 report={...report,checked_at:new Date().toISOString(),private_artifacts:dir,retained_containers:created.filter(x=>x!=='network'),network:created.includes('network')?network:null,stop_failures:stopFailures};
 if(stopFailures.length)report.passed=false;
 writeFileSync(join(dir,'report.json'),JSON.stringify(report,null,2)+'\n',{mode:0o600});
 console.log(JSON.stringify(report));process.exitCode=report.passed?0:1;
}
