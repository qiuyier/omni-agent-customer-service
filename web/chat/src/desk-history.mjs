// Merge only adjacent ID-cursor pages. Never sort by wall-clock timestamps.
export function mergeDeskHistory(current,page,direction='after'){
 if(!current||current.conversation_id!==page.conversation_id)return page;
 const incoming=page.messages||[],previous=current.messages||[];
 const ordered=direction==='before'?[...incoming,...previous]:[...previous,...incoming];
 const byID=new Map();for(const m of ordered)byID.set(m.id,m);
 const refreshed=new Set(incoming.map(m=>m.id));
 const unread=new Set((current.unread_message_ids||[]).filter(id=>!refreshed.has(id)));
 for(const id of page.unread_message_ids||[])unread.add(id);
 return {...page,messages:[...byID.values()],unread_message_ids:[...unread],
  has_older:direction==='before'?page.has_older:current.has_older,
  has_newer:direction==='before'?current.has_newer:page.has_newer};
}
export function deskTailCursor(selected){
 const messages=selected?.messages||[];return messages.length?{after_message_id:messages[messages.length-1].id}:{};
}
