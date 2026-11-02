import test from 'node:test';
import assert from 'node:assert/strict';
import {matchingFAQIndices} from './admin-search.mjs';
import {AdminController} from './admin.mjs';
const entries=[{id:'one',title:'服务时间',content:'工作日开放',source:'manual',keywords:['Hours']},{id:'two',title:'账户访问',content:'Reset password',source:'guide',keywords:['登录']}];
test('search matches title, answer and individual keywords, ignoring case and outer whitespace',()=>{
 for(const [query,want] of [['服务',[0]],[' RESET ',[1]],['hours',[0]],['登录',[1]],['',[0,1]],['  ',[0,1]],['不存在',[]],['manual',[]],['.*',[]]])assert.deepEqual(matchingFAQIndices(entries,query),want);
 assert.deepEqual(matchingFAQIndices([],''),[]);
});
test('filter keeps original indices and never mutates draft, even for duplicate IDs',()=>{
 const rows=structuredClone(entries);rows[1].id=rows[0].id;
 const before=structuredClone(rows);Object.freeze(rows);rows.forEach(row=>{Object.freeze(row.keywords);Object.freeze(row);});
 assert.deepEqual(matchingFAQIndices(rows,'登录'),[1]);assert.deepEqual(rows,before);
});
test('filtering edited draft preserves all entries and save sends the entire draft',async()=>{
 const user={id:'reviewer',version:'session',permissions:['knowledge.read','knowledge.edit']};
 let saved=null,revision=1;
 const model=new AdminController(async(path,body)=>{
  if(path==='/auth/me')return user;
  if(path.endsWith('/state'))return {revision,active:'',draft:{version:'draft',entries:saved||entries},versions:[],events:[]};
  saved=structuredClone(body.entries);return {revision:++revision};
 });
 await model.open();model.edit(model.state.entries.map((entry,index)=>index===0?{...entry,content:'未保存答案'}:entry));
 const snapshot=model.snapshot();
 assert.deepEqual(matchingFAQIndices(model.state.entries,'登录'),[1]);
 assert.deepEqual(matchingFAQIndices(model.state.entries,'未保存'),[0]);
 assert.deepEqual(matchingFAQIndices(model.state.entries,'不存在'),[]);
 assert.equal(model.snapshot(),snapshot);assert.equal(model.state.dirty,true);
 await model.mutate('draft');assert.equal(saved.length,2);assert.equal(saved[0].content,'未保存答案');assert.equal(saved[1].id,'two');
});
