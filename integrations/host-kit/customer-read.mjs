// Convert the local callback decision to the existing signed-reply wire format.
export function replyText(result){
  // Compatibility with the first Node host callback (null or text).
  if(result===null || typeof result==='string')result=result===null?{kind:'pass'}:{kind:'answer',text:result};
  if(result && Object.getPrototypeOf(result)===Object.prototype){
    const keys=Object.keys(result).sort().join(',');
    if(result.kind==='pass' && keys==='kind')return null;
    if(keys==='kind,text' && ['clarify','answer','unavailable'].includes(result.kind) &&
       typeof result.text==='string' && result.text.trim() && Buffer.byteLength(result.text)<=1024)return result.text;
  }
  throw Object.assign(Error('invalid_host_reply'),{status:503,code:'invalid_host_reply'});
}
