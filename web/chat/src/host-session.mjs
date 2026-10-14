// A public login-version nonce pins requests, but never authenticates them.
export function createPinnedFetch({version,isCurrent,onUnauthorized,fetcher=globalThis.fetch}) {
  return async (path,options={})=>{
    if(!isCurrent())throw new DOMException('Identity changed','AbortError');
    if(typeof path!=='string'||!path.startsWith('/')||path.startsWith('//')||/[\\\s\u0000-\u001f]/.test(path))throw Error('same-origin path required');
    const headers=new Headers(options.headers);headers.set('X-Business-Session',version);
    const response=await fetcher(path,{...options,headers,credentials:'same-origin',redirect:'error'});
    if(!isCurrent())throw new DOMException('Identity changed','AbortError');
    if(response.status===401)onUnauthorized();
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
