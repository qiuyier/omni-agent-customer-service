const statuses=new Set(['waiting_human','human_active','closed']);
const fail=()=>Object.assign(Error('desk_identity_changed'),{status:409});
export async function readDeskSearch(request,agentID,query,signal){
 const before=await request('/desk/me',{signal});
 if(!agentID||before.agent?.id!==agentID)throw fail();
 const page=await request('/desk/search',{method:'POST',body:{...query,expected_agent_id:agentID},signal});
 const after=await request('/desk/me',{signal});
 if(after.agent?.id!==agentID)throw fail();
 if(!page||!Array.isArray(page.items)||page.items.length>20||page.next!==undefined&&typeof page.next!=='string')throw Error('invalid_search');
 let previous=query.after||'';
 for(const row of page.items){
  if(typeof row?.conversation_id!=='string'||!row.conversation_id||row.conversation_id<=previous||!statuses.has(row.status)||!Number.isSafeInteger(row.revision)||row.revision<1)throw Error('invalid_search');
  previous=row.conversation_id;
 }
 if(page.next&&(page.items.length!==20||page.next!==previous))throw Error('invalid_search');
 return page;
}
export function validSearchQuery(q){
 return typeof q.keyword==='string'&&new TextEncoder().encode(q.keyword).length<=256&&!q.keyword.includes('\0')&&typeof q.conversation_id==='string'&&new TextEncoder().encode(q.conversation_id).length<=128&&!q.conversation_id.includes('\0')&&(q.status===''||statuses.has(q.status));
}
