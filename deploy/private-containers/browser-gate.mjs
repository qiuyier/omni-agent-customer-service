// Test-only control: no HTTP endpoint, no credentials or message bodies.
export function browserGate({readMode,log=()=>{},timeout=120000}){
 return async(req,res)=>{
  const selected=()=>{
   const mode=readMode();
   return mode==='renewal'&&req.url==='/host/chat-session'||mode==='send'&&req.url==='/chat/v1/messages'&&req.method==='POST';
  };
  if(!selected())return true;
  log({gate:'held',method:req.method,path:req.url});
  return new Promise(resolve=>{
   let done=false;
   const finish=(allowed)=>{
    if(done)return;done=true;clearInterval(poll);clearTimeout(deadline);res.removeListener('close',closed);
    log({gate:allowed?'released':'cancelled',method:req.method,path:req.url});resolve(allowed);
   };
   const closed=()=>finish(false);
   const poll=setInterval(()=>{try{if(!selected())finish(true);}catch{finish(false);}},50);
   const deadline=setTimeout(()=>finish(false),timeout);
   res.once('close',closed);
   if(res.destroyed)finish(false);
  });
 };
}
