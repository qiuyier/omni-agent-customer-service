export const maxCaseBytes=128<<10;
const textOK=(s,n)=>typeof s==='string'&&!!s.trim()&&[...s].length<=n&&!/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f-\u009f\ud800-\udfff]/u.test(s);
const idOK=(s,n)=>textOK(s,n)&&s===s.trim()&&!/[\r\n\t]/.test(s);
export const caseTemplate=[{id:'hours',query:'请问服务时间是什么？',expected_id:'service-hours'},{id:'no-match',query:'虚构的无关问题',expected_id:null}];
export function parseCases(text){
 if(new TextEncoder().encode(text).length>maxCaseBytes)throw Error('用例文件不能超过 128 KiB。');
 let cases;try{cases=JSON.parse(text);}catch{throw Error('请提供 UTF-8 JSON 数组，不能含 BOM 或尾随内容。');}
 if(!Array.isArray(cases)||cases.length<1||cases.length>50)throw Error('每批需要 1–50 条用例。');
 const ids=new Set();
 cases.forEach((c,i)=>{
  const fail=message=>{throw Error('第 '+(i+1)+' 条：'+message);};
  if(!c||typeof c!=='object'||Array.isArray(c)||Object.keys(c).some(k=>!['id','query','expected_id'].includes(k)))fail('仅允许 id、query、expected_id。');
  if(!idOK(c.id,64)||ids.has(c.id))fail('id 必须唯一、非空、最多 64 字符且无首尾空白或控制字符。');ids.add(c.id);
  if(!textOK(c.query,512))fail('query 必须非空、最多 512 字符且无非法控制字符。');
  if(c.expected_id!==null&&!idOK(c.expected_id,128))fail('expected_id 必须是 FAQ ID；预期不命中请显式填写 null。');
 });
 if(new TextEncoder().encode(JSON.stringify(cases)).length>maxCaseBytes)throw Error('序列化后的用例超过 128 KiB。');
 return cases;
}
export async function readCases(file){
 if(!file||file.size>maxCaseBytes)throw Error('用例文件不能超过 128 KiB。');
 let bytes;try{bytes=await file.arrayBuffer();}catch{throw Error('文件读取失败，请重新选择。');}
 if(bytes.byteLength>maxCaseBytes)throw Error('用例文件不能超过 128 KiB。');
 let text;try{text=new TextDecoder('utf-8',{fatal:true,ignoreBOM:true}).decode(bytes);}catch{throw Error('文件必须为有效 UTF-8。');}return parseCases(text);
}
export function validateBatch(result,revision,cases){
 if(result?.revision!==revision||!Array.isArray(result.rows)||result.rows.length!==cases.length)throw Error('invalid batch');
 for(const key of ['draft_version','published_version'])if(result[key]!==null&&(typeof result[key]!=='string'||!result[key]))throw Error('invalid version');
 result.rows.forEach((row,i)=>{if(row.id!==cases[i].id)throw Error('invalid case');
 for(const key of ['draft','published']){const o=row[key];if(!o||!['pass','fail','unavailable'].includes(o.status)||!Array.isArray(o.matched_ids)||o.matched_ids.length>3||!o.matched_ids.every(id=>typeof id==='string'))throw Error('invalid outcome');if((result[key+'_version']===null)!==(o.status==='unavailable'))throw Error('invalid availability');}
 if(row.regression!==(row.published.status==='pass'&&row.draft.status==='fail'))throw Error('invalid regression');
 });return result;
}
export function batchSummary(report){return {passed:report.rows.filter(r=>r.draft.status==='pass').length,failed:report.rows.filter(r=>r.draft.status==='fail').length,unavailable:report.rows.filter(r=>r.draft.status==='unavailable').length,regressions:report.rows.filter(r=>r.regression).length};}
