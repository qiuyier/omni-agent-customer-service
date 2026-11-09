import test from 'node:test';
import assert from 'node:assert/strict';
import {AdminController} from './admin.mjs';
const user={version:'fixture',permissions:['knowledge.read']};
const item={id:'question',revision:1,expires_at:new Date(Date.now()+3600000).toISOString()};
const match={id:'faq',title:'Title',content:'Answer',source:'fixture',keywords:['help']};
function rig(outcome='matched'){
 const calls=[];const result={id:item.id,item_revision:1,revision:2,expected_id:'faq',outcome,published:{version:'published',matches:outcome==='miss'?[]:[{...match,id:outcome==='other'?'other':'faq'}]}};
 const model=new AdminController(async(p,b)=>{calls.push({p,b});return p==='/auth/me'?user:result;});
 model.set({user,feedback:[item],data:{revision:2,published:{version:'published',entries:[match]}}});return {model,calls,result};
}
for(const outcome of ['matched','other','miss'])test('read-only check '+outcome,async()=>{const {model,calls}=rig(outcome);await model.checkFeedback(item,'faq');assert.equal(model.state.feedbackCheck.outcome,outcome);assert.equal(calls.filter(c=>c.b).length,1);assert.equal(calls.find(c=>c.b).p,'/internal/knowledge/feedback/check');assert.equal(Object.hasOwn(calls.find(c=>c.b).b,'query'),false);assert.equal(model.state.dirty,false);});
test('late results after logout are discarded',async()=>{const {model,result}=rig();let release;model.api=async p=>p==='/auth/me'?user:new Promise(r=>release=r);const pending=model.checkFeedback(item,'faq');await Promise.resolve();await Promise.resolve();model.clear();release(result);await pending;assert.equal(model.state.feedbackCheck,undefined);assert.equal(model.state.user,null);});
test('version and malformed outcomes fail closed',async()=>{for(const patch of [{revision:3},{outcome:'miss'}]){const {model,result}=rig();Object.assign(result,patch);await model.checkFeedback(item,'faq');assert.equal(model.state.feedbackCheck,null);assert.match(model.state.notice,/验证未完成/);}});
test('knowledge or list refresh invalidates result',async()=>{const {model}=rig();await model.checkFeedback(item,'faq');model.set({data:{...model.state.data,revision:3}});assert.equal(model.state.feedbackCheck,null);});
test('missing permission or expired item never sends request',async()=>{const {model,calls}=rig();model.set({user:{...user,permissions:[]}});await model.checkFeedback(item,'faq');assert.equal(calls.length,0);});
