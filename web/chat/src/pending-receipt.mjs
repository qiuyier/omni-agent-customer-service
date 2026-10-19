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
