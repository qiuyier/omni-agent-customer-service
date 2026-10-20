// Test-harness diagnostics only: never retain headers, bodies, tokens or raw errors.
const codes=new Set(['ECONNRESET','ECONNREFUSED','ENOTFOUND','EAI_AGAIN','ETIMEDOUT','EPIPE','UND_ERR_SOCKET','UND_ERR_CONNECT_TIMEOUT','UND_ERR_HEADERS_TIMEOUT']);
const names=new Set(['TypeError','SyntaxError','AbortError','TimeoutError','Error']);
export function beginTrace(id,method,path,now=Date.now()){
 return {request_id:id,method:['GET','POST','HEAD','OPTIONS'].includes(method)?method:'OTHER',path,started_at:new Date(now).toISOString(),stage:'received'};
}
export function failTrace(entry,error,now=Date.now()){
 entry.failed_stage=entry.stage;entry.stage='proxy_error';
 entry.error_name=names.has(error?.name)?error.name:'OtherError';
 const code=error?.cause?.code??error?.code;entry.error_code=codes.has(code)?code:'OTHER';
 entry.duration_ms=Math.max(0,now-Date.parse(entry.started_at));
}
export function recordAction(entry,body){
 try{const x=JSON.parse(body);if(['history','claim','close','reply','read'].includes(x?.action))entry.action=x.action;
 if(Number.isSafeInteger(x?.revision)&&x.revision>0)entry.request_revision=x.revision;
 }catch{}
}
