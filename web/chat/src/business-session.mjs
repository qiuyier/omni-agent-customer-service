// A public login-version nonce pins requests, but never authenticates them.
export function createPinnedFetch({version,isCurrent,onUnauthorized,fetcher=globalThis.fetch}) {
  return async (path,options={})=>{
    if(!isCurrent())throw new DOMException('Identity changed','AbortError');
    const headers=new Headers(options.headers);headers.set('X-Business-Session',version);
    const response=await fetcher(path,{...options,headers});
    if(!isCurrent())throw new DOMException('Identity changed','AbortError');
    if(response.status===401)onUnauthorized();
    return response;
  };
}
