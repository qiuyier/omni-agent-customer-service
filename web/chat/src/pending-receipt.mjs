import {readPending,pendingKey} from './protocol.mjs';
// Acceptance only, not proof of a bot/human answer. Never invokes send().
export async function reconcilePending({api,storage,conversationID,pending,signal,active,onReplyState=()=>{}}){
 if(!pending||!active())return false;
 const result=await api.receipt(pending,signal);
 if(!active()||signal?.aborted)return false;
 if(result?.conversation_id!==conversationID||result.event_id!==pending.event_id||result.accepted!==true||typeof result.message_id!=='string'||!result.message_id)return false;
 const current=readPending(storage,conversationID);
 if(current?.event_id!==pending.event_id||current.content!==pending.content)return false;
 const state=receiptReplyState(result);
 onReplyState(state);
 if(state==='retry'||state==='unknown')return false;
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

// Eligibility is a server snapshot, not permission to bypass the send checks.
export function receiptReplyState(result){
 if(result?.reply_status==='not_replied'&&result.retry_allowed===true)return 'retry';
 if(result?.reply_status==='replied'&&result.retry_allowed===false)return 'replied';
 if(result?.reply_status==='unavailable'&&result.retry_allowed===false)return 'unavailable';
 return 'unknown';
}
