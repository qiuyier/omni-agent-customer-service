export const diffFields=['title','content','source','keywords'];
// Compare saved values, not the editable/filterable list. IDs identify entries.
export function publicationDiff(data) {
 if(!data?.draft)return null;
 if(data.active && (data.published?.version!==data.active || !Array.isArray(data.published?.entries)))return null;
 const before=data.published?.entries||[],after=data.draft.entries;
 const old=new Map(before.map(e=>[e.id.trim(),e])),next=new Map(after.map(e=>[e.id.trim(),e]));
 const changes=[];
 for(const entry of after){const previous=old.get(entry.id.trim());const fields=previous?diffFields.filter(k=>JSON.stringify(previous[k])!==JSON.stringify(entry[k])):diffFields;
  if(!previous||fields.length)changes.push({id:entry.id.trim(),kind:previous?'modified':'added',before:previous||null,after:entry,fields});
 }
 for(const entry of before)if(!next.has(entry.id.trim()))changes.push({id:entry.id.trim(),kind:'removed',before:entry,after:null,fields:diffFields});
 return {changes,added:changes.filter(c=>c.kind==='added').length,modified:changes.filter(c=>c.kind==='modified').length,removed:changes.filter(c=>c.kind==='removed').length,empty:after.length===0};
}
