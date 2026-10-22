// A public login-version nonce pins requests, but never authenticates them.
export function createPinnedFetch({version,isCurrent,onUnauthorized,identity,identityURL,fetcher=globalThis.fetch}) {
  const chatPaths=new Set(['/chat/v1/messages','/chat/v1/message-receipt','/chat/v1/handoff','/chat/v1/consultations','/chat/v1/previous-consultation']);
  if(identityURL!==undefined&&(!/^\/(?!\/)[A-Za-z0-9_./-]*$/.test(identityURL)||identityURL.includes('..')))throw Error('same-origin identity path required');
  return async (path,options={})=>{
    const {chatTokenRecovery=false,...requestOptions}=options;
    const check=()=>{if(!isCurrent()||options.signal?.aborted)throw new DOMException('Identity changed','AbortError');};
    if(!isCurrent())throw new DOMException('Identity changed','AbortError');
    if(typeof path!=='string'||!path.startsWith('/')||path.startsWith('//')||/[\\\s\u0000-\u001f]/.test(path))throw Error('same-origin path required');
    const headers=new Headers(options.headers);headers.set('X-Business-Session',version);
    const response=await fetcher(path,{...requestOptions,headers,credentials:'same-origin',redirect:'error'});
    if(!isCurrent())throw new DOMException('Identity changed','AbortError');
    if(response.status===401){
      // Only the first attempt of a chat operation may recover. No renewal of
      // business sessions, staff authority or a failed second chat attempt.
      if(chatTokenRecovery===true&&identity?.role==='customer'&&identity.version===version&&identityURL&&chatPaths.has(path)&&headers.has('Authorization')){
        let verified;
        try{
          const r=await fetcher(identityURL,{method:'GET',headers:{Accept:'application/json','X-Business-Session':version},credentials:'same-origin',redirect:'error',cache:'no-store',signal:options.signal});
          check();
          if(!r.ok&&r.status!==401&&r.status!==403)throw Error('identity unavailable');
          verified=r.ok?readHostIdentity(await r.json(),'customer'):null;
        }catch(error){
          check();
          // Unverifiable identity never authorizes a resend; preserve pending
          // content and report a service error instead of claiming logout.
          return new Response('{"error":"unavailable"}',{status:503,headers:{'Content-Type':'application/json'}});
        }
        check();
        if(verified?.id===identity.id&&verified?.version===version)return response;
      }
      onUnauthorized();
    }
    return response;
  };
}

// Explicit, same-origin contract. No business-specific routes or credentials.
export function createHostSessionConfig(input) {
  if(!input||!['customer','staff'].includes(input.role))throw Error('invalid host role');
  const result={};
  for(const key of ['identityURL','logoutURL','loginURL']){
    const value=input[key];
    if(typeof value!=='string'||!/^\/(?!\/)[A-Za-z0-9_./-]*$/.test(value)||value.includes('..'))throw Error('invalid host path');
    result[key]=value;
  }
  if(typeof input.channelName!=='string'||!/^[A-Za-z0-9:_-]{1,100}$/.test(input.channelName))throw Error('invalid host channel');
  return Object.freeze({...result,role:input.role,channelName:input.channelName,label:input.label||'业务系统',notice:input.notice||''});
}
export function readHostIdentity(data,role) {
  if(data?.identity===null)return null;
  const identity=data?.identity;
  if(!identity||identity.role!==role||typeof identity.id!=='string'||!/^[A-Za-z0-9_-]{1,128}$/.test(identity.id)||typeof identity.version!=='string'||!/^[A-Za-z0-9_-]{16,128}$/.test(identity.version))throw Error('invalid host identity');
  return {id:identity.id,role,version:identity.version};
}
export function announceHostIdentityChange(config,Channel=globalThis.BroadcastChannel) {
  try{const channel=new Channel(config.channelName);try{channel.postMessage({role:config.role});}finally{channel.close();}}catch{/* Focus and polling remain available. */}
}
