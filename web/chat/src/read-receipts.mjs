// Exact, visible message acknowledgements. No cursor or implicit history acknowledgement.
export function createReadReceipts({send,visible,active,onError=()=>{},schedule=setTimeout,cancel=clearTimeout}){
 const seen=new Set(),onScreen=new Set();let stopped=false,timer=null,inFlight=false,failed=false;
 const eligible=()=>!stopped&&active()&&visible();
 function queue(){if(!eligible()||failed||inFlight||timer!==null||![...onScreen].some(id=>!seen.has(id)))return;timer=schedule(flush,300);}
 async function flush(){
  timer=null;if(!eligible()||inFlight||failed)return;
  const ids=[...onScreen].filter(id=>!seen.has(id)).slice(0,100);if(!ids.length)return;inFlight=true;
  try{await send(ids);if(!stopped)for(const id of ids)seen.add(id);}
  catch(e){if(!stopped&&e.name!=='AbortError'){failed=true;onError(e);}}
  finally{inFlight=false;queue();}
 }
 return {intersection(id,intersecting){if(stopped)return;if(intersecting)onScreen.add(id);else onScreen.delete(id);queue();},visibility:queue,stop(){stopped=true;if(timer!==null)cancel(timer);onScreen.clear();}};
}
