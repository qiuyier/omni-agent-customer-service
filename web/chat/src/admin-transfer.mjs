import {publicationDiff} from './admin-diff.mjs';
export const maxImportBytes=1<<20;
export const templateEntries=[{id:'service-hours',title:'服务时间是什么？',content:'示例：工作日 09:00–17:00。请替换为已核实的业务资料。',source:'示例服务手册',keywords:['服务时间','营业时间']}];
const fields=['id','title','content','source','keywords'];
const size=text=>new TextEncoder().encode(text).length;
// Match Go unicode.IsSpace, including NEL but excluding BOM.
const trim=text=>text.replace(/^[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+|[\u0009-\u000d\u0020\u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+$/g,'');
function validText(value,limit,multiline=false){
 if(typeof value!=='string'||!trim(value)||[...value].length>limit)return false;
 for(const char of value){const cp=char.codePointAt(0);if(cp>=0xd800&&cp<=0xdfff)return false;if((cp<=31||cp>=127&&cp<=159)&&!(multiline&&['\n','\r','\t'].includes(char)))return false;}
 return true;
}
export function parseDraftJSON(text){
 if(typeof text!=='string'||size(text)>maxImportBytes)throw Error('文件不得超过 1 MiB。');
 let entries;try{entries=JSON.parse(text);}catch{throw Error('JSON 格式错误：请使用 UTF-8 编码的单个数组，不含 BOM 或尾随内容。');}
 if(!Array.isArray(entries))throw Error('顶层必须是 FAQ 数组，例如 []，不能是对象或 null。');
 if(entries.length>1000)throw Error('FAQ 最多 1000 条。');
 const ids=new Set();
 entries.forEach((entry,index)=>{
  const fail=message=>{throw Error('第 '+(index+1)+' 条：'+message);};
  if(!entry||typeof entry!=='object'||Array.isArray(entry))fail('必须是 FAQ 对象。');
  if(Object.keys(entry).some(key=>!fields.includes(key)))fail('包含未知字段；只允许 id、title、content、source、keywords。');
  for(const [field,limit] of [['id',128],['title',128],['source',256],['content',6000]])if(!validText(entry[field],limit,field==='content'))fail(field+' 必须非空，最多 '+limit+' 个字符，且不能含非法控制字符。');
  if(['id','title','source','content'].reduce((n,key)=>n+[...trim(entry[key])].length,0)>6000)fail('ID、标题、来源和答案合计超过 6000 字符。');
  const id=trim(entry.id);if(ids.has(id))fail('id 与前面的条目重复（忽略首尾空白）。');ids.add(id);
  if(!Array.isArray(entry.keywords)||entry.keywords.length<1||entry.keywords.length>20)fail('keywords 必须是包含 1–20 个字符串的数组。');
  entry.keywords.forEach((word,i)=>{if(!validText(word,64))fail('keywords 第 '+(i+1)+' 项必须非空、最多 64 字符，且不能含控制字符。');});
 });
 // Saving uses JSON.stringify; check that escaped serialization also fits.
 if(size(JSON.stringify(entries))>maxImportBytes)throw Error('序列化后的 FAQ 超过 1 MiB，无法保存。');
 return entries;
}
export async function readDraftFile(file){
 if(!file||file.size>maxImportBytes)throw Error('文件不得超过 1 MiB。');
 let bytes;try{bytes=await file.arrayBuffer();}catch{throw Error('无法读取文件，请重新选择。');}
 if(bytes.byteLength>maxImportBytes)throw Error('文件不得超过 1 MiB。');
 let text;try{text=new TextDecoder('utf-8',{fatal:true,ignoreBOM:true}).decode(bytes);}catch{throw Error('文件必须使用有效 UTF-8 编码。');}
 return parseDraftJSON(text);
}
export function canImport(s){return !!(s.user?.permissions.includes('knowledge.edit')&&s.user.permissions.includes('knowledge.read')&&s.data&&!s.dirty&&!s.busy&&!s.checking&&!s.blocked);}
export function importStillCurrent(s,base){return canImport(s)&&s.user.version===base.user.version&&s.data===base.data&&s.entries===base.entries;}
export function importDifference(before,after){return publicationDiff({active:'baseline',published:{version:'baseline',entries:before},draft:{entries:after}});}
export function applyDraftImport(model,base,entries){
 if(!importStillCurrent(model.state,base))return false;
 const validated=parseDraftJSON(JSON.stringify(entries));
 model.edit(validated);
 model.set({notice:'已导入本地草稿，尚未保存或发布。请核对内容后保存草稿。'});
 return true;
}
export function exportSavedDraft(s){
 if(!s.user?.permissions.includes('knowledge.read')||!s.data?.draft||s.checking||s.busy||s.blocked)return null;
 // Compact JSON preserves the import byte limit; omit all management metadata.
 return JSON.stringify(s.data.draft.entries);
}
