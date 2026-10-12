// Completion-based scheduling: at most one read, no catch-up bursts, no writes.
export function createPoller({read,onState=()=>{},visible=()=>true,subscribe=()=>()=>{},schedule=setTimeout,unschedule=clearTimeout,interval=5000,maxDelay=60000}) {
 let stopped=false,timer=null,flight=null,epoch=0,failures=0;
 const arm=(delay=interval)=>{if(stopped||!visible()||flight)return;unschedule(timer);timer=schedule(run,delay);};
 const invalidate=()=>{epoch++;unschedule(timer);timer=null;flight?.abort();};
 async function run(){
  timer=null;if(stopped||!visible()||flight)return;
  const controller=new AbortController(),version=epoch;flight=controller;
  const active=()=>!stopped&&!controller.signal.aborted&&version===epoch&&visible();
  try{await read({signal:controller.signal,active});if(active()){failures=0;onState('active');}}
  catch(e){if(active()&&e.name!=='AbortError'){failures++;onState('retrying');}}
  finally{flight=null;arm(Math.min(maxDelay,interval*2**Math.min(failures,4)));}
 }
 const visibility=()=>{invalidate();onState(visible()?'active':'paused');if(visible())arm(0);};
 const unsubscribe=subscribe(visibility);onState(visible()?'active':'paused');arm();
 return {invalidate:()=>{invalidate();arm();},stop:()=>{stopped=true;invalidate();unsubscribe();}};
}

// Staff history is a read-only action despite its existing POST transport.
// Check the actor before ever requesting a selected conversation's history.
export async function readDeskSnapshot(request,agentID,conversationID,signal,historyCursor={}){
 const me=await request('/desk/me',{signal});
 if(me.agent?.id!==agentID)return {me,changed:true};
 if(!me.agent)return {me,queue:[],selected:null};
 const queue=await request('/desk/queue',{signal});
 let selected=null;
 if(conversationID){selected=await request('/desk/action',{method:'POST',body:{...historyCursor,conversation_id:conversationID,action:'history'},signal});if(selected?.conversation_id!==conversationID)throw Error('invalid_history');}
 if(!Array.isArray(queue.items))throw Error('invalid_queue');
 // Another tab may switch the staff cookie while these reads are in flight.
 const confirmed=await request('/desk/me',{signal});
 if(confirmed.agent?.id!==agentID)return {me:confirmed,changed:true};
 return {me,queue:queue.items,selected};
}
