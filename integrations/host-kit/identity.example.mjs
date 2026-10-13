// Wire these callbacks to your EXISTING verified business session service.
// Do not return IDs from body/query/header claims without authentication.
export function businessIdentity({sessions}){
 async function authenticate(kind,request){
  // Implement this trusted service: verify session signature/cache, expiry,
  // enabled account and role. It must support revocation on every call.
  const login=await sessions.authenticate(request,kind);
  if(!login||login.role!==kind||login.enabled!==true)return null;
  return {id:login.subjectID,name:login.displayName,sessionID:login.sessionID,expiresAt:login.expiresAt};
 }
 return {customer:req=>authenticate('customer',req),staff:req=>authenticate('staff',req)};
}
// startAdapter({gatewayOrigin, hostSecret, agents, identity:businessIdentity({sessions})})
// Keep original login/logout routes on the business backend. No demoAccounts.
