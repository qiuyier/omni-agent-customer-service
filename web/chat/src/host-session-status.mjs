// These states only explain why identity is hidden; none authorizes access.
export function hostIdentityFailureStatus(error,online=true) {
  if(error?.status===401||error?.status===403)return 'unauthorized';
  return online===false?'offline':'unavailable';
}
const states=Object.freeze({
  checking:Object.freeze({message:'正在核对业务登录身份…',retry:false}),
  offline:Object.freeze({message:'网络暂不可用，无法核对登录身份。消息已隐藏；网络恢复后会自动重新核对。',retry:true}),
  unavailable:Object.freeze({message:'暂时无法核对登录身份，可能是网络或服务异常。消息已隐藏；将自动重试，也可立即重新核对。',retry:true}),
  unauthorized:Object.freeze({message:'登录已失效或无访问权限，请重新进入业务登录。',retry:false}),
  signingOut:Object.freeze({message:'正在退出业务登录，消息已隐藏…',retry:false}),
  logoutFailed:Object.freeze({message:'退出未确认，身份内容已隐藏。请重试退出或返回业务登录。',retry:false}),
});
export function hostSessionStatusView(status){return states[status]||states.unavailable;}
