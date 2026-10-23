export function parseArgs(args){
 const allowed=new Set(['--postgres','--allow-test-writes','--help']);
 if(args.some(x=>!allowed.has(x))||new Set(args).size!==args.length)throw Error('Unknown or duplicate option; use --help');
 const postgres=args.includes('--postgres'),writes=args.includes('--allow-test-writes');
 if(postgres!==writes&&!args.includes('--help'))throw Error('PostgreSQL requires both --postgres and --allow-test-writes');
 return {postgres,help:args.includes('--help')};
}
export function cleanEnvironment(source){
 const result={};
 for(const key of ['PATH','HOME','TMPDIR','GOCACHE','GOMODCACHE','GOPATH','GOROOT','DOCKER_CONFIG'])if(source[key])result[key]=source[key];
 result.GOENV='off';result.GOTOOLCHAIN='local';return result;
}
export function assess(result,format){
 let passed=0,skipped=0,failed=0;
 if(format==='go'){
  for(const line of result.text.split('\n')){try{const x=JSON.parse(line);if(!x.Test)continue;if(x.Action==='pass')passed++;if(x.Action==='skip')skipped++;if(x.Action==='fail')failed++;}catch{}}
 }else{
  const count=key=>Number(result.text.match(new RegExp('^# '+key+' (\\d+)$','m'))?.[1]||0);
  passed=count('pass');skipped=count('skipped')+count('todo');failed=count('fail')+count('cancelled');
 }
 return {status:result.code===0&&!result.timedOut&&!result.spawnError&&passed>0&&skipped===0&&failed===0?'passed':'failed',passed,skipped,failed,exit_code:result.code,signal:result.signal||null,timed_out:!!result.timedOut,error:result.spawnError||null};
}
