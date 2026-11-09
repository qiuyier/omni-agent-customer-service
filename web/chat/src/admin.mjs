import {parseCases,validateBatch} from './admin-batch.mjs';
const messages = {
 invalid_query:'试问必须为 1–512 个字符，且不能含非法控制字符。',
 revision_conflict:'其他管理员已更新。你的编辑仍保留；核对最新状态后再决定是否放弃本地编辑。',
 outcome_unknown_reload_required:'结果未知：请求可能已经生效。请先核对服务器状态，不要重复提交。',
 history_limit:'历史记录已达上限，请联系维护者，不要重复提交。',
 empty_confirmation_required:'目标版本为空。确认要停用全部 FAQ 后，勾选空版本确认再操作。',
 invalid_request:'内容不符合 FAQ 规则，请检查必填字段、长度和重复 ID。',
 storage_unavailable:'存储暂不可用，请稍后手动核对状态。',
 version_not_found:'目标版本不存在，请重新读取状态。',
 busy:'请求过多，请稍后手动重试。',
};
export class APIError extends Error {
 constructor(status,code) { super(messages[code] || (status===429?'请求过多，请稍后再试。':'请求未完成，请检查服务后重试。')); this.status=status; this.code=code; }
}
export async function request(path, body, version, fetcher=fetch) {
 const controller=new AbortController(); const timer=setTimeout(()=>controller.abort(),15000);
 try {
  const headers={}; if(body!==undefined) headers['Content-Type']='application/json';
  if(version) headers['X-Knowledge-Session']=version;
  const response=await fetcher(path,{method:body===undefined?'GET':'POST',headers,body:body===undefined?undefined:JSON.stringify(body),credentials:'same-origin',cache:'no-store',redirect:'error',signal:controller.signal});
  if(response.status===204)return null;
  let data; try { data=await response.json(); } catch { throw new APIError(0,'outcome_unknown_reload_required'); }
  if(!response.ok)throw new APIError(response.status,data.code);
  return data;
 } finally { clearTimeout(timer); }
}
const fresh=()=>({user:null,data:null,entries:[],busy:false,checking:false,blocked:false,review:null,notice:'',dirty:false,trial:null,batch:null,batchCases:null,feedback:null});
export class AdminController {
 constructor(api=request){this.api=api;this.state=fresh();this.epoch=0;this.listeners=new Set();}
 subscribe=(fn)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn);};
 snapshot=()=>this.state;
 set(patch){this.state={...this.state,...patch};for(const fn of this.listeners)fn();}
 clear(notice='登录已失效或账号发生变化，请重新登录。'){this.epoch++;this.state={...fresh(),notice};for(const fn of this.listeners)fn();}
 can(permission){return this.state.user?.permissions.includes(`knowledge.${permission}`);}
 async identity(epoch, expected){
  const user=await this.api('/auth/me');
  if(epoch!==this.epoch)throw new Error('stale');
  if(!user?.version || !Array.isArray(user.permissions) || expected && expected!==user.version){this.clear();throw new Error('stale');}
  return user;
 }
 async open(){
  if(this.state.busy)return;
  const epoch=++this.epoch;this.set({checking:true,notice:'',feedback:null});
  try {
   const user=await this.identity(epoch,this.state.user?.version);
   if(!user.permissions.includes('knowledge.read')){this.set({user,checking:false,notice:'此账号没有知识读取权限，请联系管理员。'});return;}
   const data=await this.api('/internal/knowledge/state',undefined,user.version);
   await this.identity(epoch,user.version);
   this.set({user,data,entries:structuredClone(data.draft?.entries||[]),checking:false,dirty:false,blocked:false,review:null});
  } catch(e){if(epoch===this.epoch)this.clear(e.status===401?'请输入独立管理员账号。':'无法确认登录或读取状态，请重新登录后重试。');}
 }
 async verify(){
  if(!this.state.user || this.state.busy || this.state.checking)return;
  const epoch=this.epoch;
  try{await this.identity(epoch,this.state.user.version);if(epoch===this.epoch)this.set({checking:false});}
  catch{if(epoch===this.epoch)this.clear();}
 }
 async login(account,password){
  if(this.state.busy)return;
  const epoch=++this.epoch;this.set({...fresh(),busy:true});
  try{await this.api('/auth/login',{account,password});if(epoch!==this.epoch)return;this.set({busy:false});await this.open();}
  catch(e){if(epoch===this.epoch)this.clear(e.status===401?'账号或密码不正确。':e instanceof APIError?e.message:'无法连接管理服务，请检查服务后重试。');}
 }
 async logout(){
  const version=this.state.user?.version;this.clear('已隐藏本地内容，正在退出…');const epoch=this.epoch;this.set({busy:true});
  try{await this.api('/auth/logout',{},version);if(epoch===this.epoch)this.set({busy:false,notice:'已退出。'});}
  catch{if(epoch===this.epoch)this.set({busy:false,notice:'内容已隐藏，但服务器退出未确认；请重新登录或关闭页面，等待会话到期。'});}
 }
 edit(entries){if(!this.state.busy && !this.state.checking && this.can('edit'))this.set({entries,dirty:true,trial:null,batch:null,batchCases:null});}
 async loadFeedback(change){
  const s=this.state;if(s.busy||s.checking||!s.user||!this.can(change?'edit':'read'))return;
  const epoch=this.epoch;this.set({busy:true,notice:'',feedback:null});
  try {
   await this.identity(epoch,s.user.version);
   if(change)await this.api('/internal/knowledge/feedback/status',change,s.user.version);
   const result=await this.api('/internal/knowledge/feedback',undefined,s.user.version);
   await this.identity(epoch,s.user.version);
   if(!Array.isArray(result?.items)||result.items.length>500||!result.items.every(i=>typeof i.id==='string'&&typeof i.question==='string'&&i.question.length<=1024&&Number.isSafeInteger(i.revision)&&i.revision>0&&['pending','ignored','resolved'].includes(i.status)&&typeof i.faq_id==='string'&&Number.isSafeInteger(i.count)&&Number.isFinite(Date.parse(i.expires_at))))throw Error('invalid feedback');
   this.set({busy:false,feedback:result.items,notice:change?'问题状态已更新。':''});
  }catch(e){if(epoch!==this.epoch)return;if(e.status===401||e.status===403){this.clear();return;}this.set({busy:false,feedback:null,notice:change?'状态更新未确认，请重新读取问题列表核对；不要直接重复提交。':'问题列表读取失败，请手动重试。'});}
 }
 feedbackToDraft(item){
  const s=this.state;if(s.busy||s.checking||s.blocked||!this.can('edit')||s.entries.length>=1000||!s.feedback?.includes(item)||Date.parse(item.expires_at)<=Date.now())return false;
  const id='feedback-'+item.id;
  if(s.entries.some(e=>e.id===id)){this.set({notice:'本地草稿已有该问题，请在编辑区检查。'});return false;}
  this.edit([...s.entries,{id,title:Array.from(item.question).slice(0,128).join(''),content:'',source:'未命中问题（待核实）',keywords:[]}]);
  this.set({notice:'已加入本地草稿。请检查隐私、补齐答案与关键词，再保存、审核和发布。原问题尚未解决。'});return true;
 }
 async mutate(action,extra={}){
  const s=this.state;
  if(s.busy||s.checking||s.blocked||!s.data||!this.can(action==='draft'?'edit':'publish'))return;
  if(action!=='draft'&&s.dirty){this.set({notice:'请先保存或放弃本地编辑，再发布或回退。'});return;}
  const epoch=this.epoch;let sent=false;this.set({busy:true,notice:'',review:null,trial:null,batch:null,batchCases:null});
  try{
   await this.identity(epoch,s.user.version);
   sent=true;
   const result=await this.api(`/internal/knowledge/${action}`,{...extra,revision:s.data.revision,...(action==='draft'?{entries:s.entries}:{})},s.user.version);
   if(!Number.isSafeInteger(result?.revision))throw new APIError(0,'outcome_unknown_reload_required');
   const data=await this.api('/internal/knowledge/state',undefined,s.user.version);
   await this.identity(epoch,s.user.version);
   this.set({data,entries:structuredClone(data.draft?.entries||[]),dirty:false,busy:false,notice:action==='draft'?'草稿已保存，尚未发布。':'操作完成，当前状态已重新读取。'});
  }catch(e){
   if(epoch!==this.epoch)return;
   if(e.status===401||e.status===403){this.clear('登录失效、账号变化或权限不足，请重新登录确认。');return;}
   const uncertain=sent && (!e.status || e.status>=500);
   this.set({busy:false,blocked:uncertain||e.code==='revision_conflict',notice:uncertain?messages.outcome_unknown_reload_required:e.message||'无法确认当前会话，请重新登录。'});
   if(!sent)this.clear();
  }
 }
 async runBatch(){
  const s=this.state;if(s.busy||s.checking||s.blocked||s.dirty||!s.data||!s.batchCases||!this.can('read'))return;
  const epoch=this.epoch;this.set({busy:true,batch:null,notice:''});
  try{
   const cases=parseCases(JSON.stringify(s.batchCases));
   await this.identity(epoch,s.user.version);
   const report=await this.api('/internal/knowledge/batch',{revision:s.data.revision,cases},s.user.version);
   await this.identity(epoch,s.user.version);
   validateBatch(report,s.data.revision,cases);
   this.set({busy:false,batch:{...report,cases,generated_at:new Date().toISOString(),schema_version:1}});
  }catch(e){if(epoch!==this.epoch)return;if(e.status===401||e.status===403){this.clear();return;}this.set({busy:false,batch:null,blocked:e.code==='revision_conflict',notice:e.code==='revision_conflict'?messages.revision_conflict:e.code==='invalid_cases'?'用例格式不符合要求，请检查后重新选择。':'批量检查未完成，请手动重试。没有保存或发布任何内容。'});}
 }
 async tryQuery(query){
  const s=this.state;if(s.busy||s.checking||s.blocked||s.dirty||!s.data||!this.can('read'))return;
  const epoch=this.epoch;this.set({busy:true,trial:null,notice:''});
  try{
   await this.identity(epoch,s.user.version);
   const result=await this.api('/internal/knowledge/trial',{revision:s.data.revision,query},s.user.version);
   await this.identity(epoch,s.user.version);
   if(result?.revision!==s.data.revision||!Object.hasOwn(result,'draft')||!Object.hasOwn(result,'published'))throw Error('invalid response');
   for(const key of ['draft','published'])if(result[key]!==null&&(!result[key]?.version||!Array.isArray(result[key].matches)))throw Error('invalid response');
   for(const key of ['draft','published'])for(const match of result[key]?.matches||[])if(!['id','title','content','source'].every(field=>typeof match[field]==='string')||!Array.isArray(match.keywords)||!match.keywords.every(word=>typeof word==='string'))throw Error('invalid match');
   this.set({busy:false,trial:{...result,query}});
  }catch(e){if(epoch!==this.epoch)return;if(e.status===401||e.status===403){this.clear();return;}this.set({busy:false,trial:null,blocked:e.code==='revision_conflict',notice:e.code==='revision_conflict'?messages.revision_conflict:e.code==='invalid_query'?messages.invalid_query:'试检索未完成，请手动重试；没有保存或发布任何内容。'});}
 }
 async inspect(){
  if(this.state.busy||!this.state.user)return;
  const epoch=this.epoch,version=this.state.user.version;this.set({busy:true,trial:null,batch:null,batchCases:null});
  try{await this.identity(epoch,version);const review=await this.api('/internal/knowledge/state',undefined,version);await this.identity(epoch,version);this.set({review,busy:false,blocked:true,notice:'已读取服务器状态；不会自动重发。核对记录后，可明确采用服务器草稿。'});}
  catch(e){if(epoch!==this.epoch)return;if(e.status===401||e.status===403)this.clear();else this.set({busy:false,notice:'核对失败，本地编辑仍保留，提交保持暂停。',blocked:true});}
 }
 adopt(){if(this.state.review&&!this.state.busy){const data=this.state.review;this.set({data,entries:structuredClone(data.draft?.entries||[]),dirty:false,blocked:false,review:null,notice:'已采用服务器草稿；如需修改，请重新编辑。'});}}
}
