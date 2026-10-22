import {readPending,pendingKey} from './protocol.mjs';
// Acceptance only, not proof of a bot/human answer. Never invokes send().
export async function reconcilePending({api,storage,conversationID,pending,signal,active}){
 if(!pending||!active())return false;
 const result=await api.receipt(pending,signal);
 if(!active()||signal?.aborted)return false;
 if(result?.conversation_id!==conversationID||result.event_id!==pending.event_id||result.accepted!==true||typeof result.message_id!=='string'||!result.message_id)return false;
 const current=readPending(storage,conversationID);
 if(current?.event_id!==pending.event_id||current.content!==pending.content)return false;
 storage.removeItem(pendingKey(conversationID));
 return true;
}

// A failed send may already be accepted. Probe only; preserve the original
// pending event for explicit processing retry and never infer a reply exists.
export async function probeAccepted({api,conversationID,pending,signal,active}){
 if(!pending||!active()||signal?.aborted)return false;
 const result=await api.receipt(pending,signal);
 return active()&&!signal?.aborted&&result?.conversation_id===conversationID&&result.event_id===pending.event_id&&result.accepted===true&&typeof result.message_id==='string'&&result.message_id.length>0;
}
