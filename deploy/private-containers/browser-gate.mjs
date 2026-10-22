// Test-only control: no HTTP endpoint, no credentials or message bodies.
export function browserGate({readMode,log=()=>{},timeout=120000}){
 return async(req,res)=>{
  if(readMode()==='drop-block'&&req.url==='/chat/v1/message-receipt'){log({gate:'receipt-blocked',method:req.method,path:req.url});return false;}
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

// Discard only a successful upstream send response, after the full body was read.
export function responseLoss({readMode,log=()=>{}}){
 return (req,status)=>{
  if(['drop','drop-block'].includes(readMode())&&req.method==='POST'&&req.url==='/chat/v1/messages'&&status===200){
   log({gate:'response-dropped',method:req.method,path:req.url,upstream_status:status});return false;
  }
  return true;
 };
}
