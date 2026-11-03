import test from 'node:test';
import assert from 'node:assert/strict';
import {AdminController} from './admin.mjs';
import {parseDraftJSON,readDraftFile,canImport,importStillCurrent,applyDraftImport,importDifference,exportSavedDraft,templateEntries,maxImportBytes} from './admin-transfer.mjs';
const entry=(id='one',extra={})=>({id,title:'标题',content:'答案',source:'手册',keywords:['帮助'],...extra});
const encode=entries=>JSON.stringify(entries);
function rig(){const model=new AdminController(()=>{throw Error('Import must not call the API');});model.set({user:{id:'editor',version:'session-a',permissions:['knowledge.read','knowledge.edit']},data:{revision:5,active:'published',draft:{entries:[entry()]},events:[]},entries:[entry()]});return model;}
test('template, multiline content, empty arrays and export/import round trips',async()=>{
 assert.deepEqual(parseDraftJSON(encode(templateEntries)),templateEntries);
 const rows=[entry('一',{content:'答案\n下一行\r\t说明'})];assert.deepEqual(parseDraftJSON(encode(rows)),rows);
 assert.deepEqual(parseDraftJSON('[]'),[]);
 const model=rig();model.edit([entry('local')]);const exported=exportSavedDraft(model.state);
 assert.deepEqual(parseDraftJSON(exported),[entry()]);assert.ok(!exported.includes('revision'));assert.ok(!exported.includes('session-a'));
 assert.deepEqual(await readDraftFile(new Blob([exported])),[entry()]);
});
test('malformed arrays, unknown fields, wrong types and trailing content fail with no parser excerpts',()=>{
 for(const text of ['{}','null','[] []','[','\ufeff[]','[null]','[[]]'])assert.throws(()=>parseDraftJSON(text));
 for(const extra of [{surprise:true},{title:null},{content:7},{keywords:'help'},{keywords:[false]},{keywords:[]},{keywords:Array(21).fill('help')},{id:''}])assert.throws(()=>parseDraftJSON(encode([entry('a',extra)])),/第 1 条/);
 assert.throws(()=>parseDraftJSON('["PRIVATE_CONTENT"'),e=>!e.message.includes('PRIVATE_CONTENT'));
 assert.throws(()=>parseDraftJSON(encode([entry('a'),entry(' a ')])),/第 2 条.*重复/);
});
test('Unicode rune counts, controls, blank fields, keyword and combined limits',()=>{
 assert.doesNotThrow(()=>parseDraftJSON(encode([entry('a',{title:'𠮷'.repeat(128)})])));
 for(const extra of [{title:'𠮷'.repeat(129)},{source:'x'.repeat(257)},{content:'x'.repeat(6000)},{keywords:['x'.repeat(65)]},{title:'\n'},{title:'\u0085'},{title:'x\u0000'},{content:'x\u000b'},{content:'\ud800'},{keywords:['a\tb']}])assert.throws(()=>parseDraftJSON(encode([entry('a',extra)])));
 assert.throws(()=>parseDraftJSON(encode([entry('a'),entry('\u00a0a\u00a0')])),/重复/);
});
test('byte, count and UTF-8 limits are checked before and after reading',async()=>{
 assert.throws(()=>parseDraftJSON(' '.repeat(maxImportBytes)+'[]'),/1 MiB/);
 assert.throws(()=>parseDraftJSON(encode(Array.from({length:1001},(_,i)=>entry('id'+i)))),/1000/);
 let read=false;await assert.rejects(readDraftFile({size:maxImportBytes+1,arrayBuffer(){read=true;}}),/1 MiB/);assert.equal(read,false);
 await assert.rejects(readDraftFile({size:1,arrayBuffer:async()=>new ArrayBuffer(maxImportBytes+1)}),/1 MiB/);
 await assert.rejects(readDraftFile(new Blob([new Uint8Array([255])])),/UTF-8/);
 await assert.rejects(readDraftFile(new Blob(['\ufeff[]'])),/JSON/);
 await assert.rejects(readDraftFile({size:1,arrayBuffer:async()=>{throw Error('private path');}}),/无法读取文件/);
});
test('preview/cancel and invalid data do not mutate; confirmed import only marks a local edit',()=>{
 const model=rig(),base=model.state,rows=[entry('two')];
 const diff=importDifference(base.entries,rows);assert.equal(diff.added,1);assert.equal(diff.removed,1);assert.equal(model.state,base);
 assert.throws(()=>parseDraftJSON('bad'));assert.equal(model.state,base);
 assert.equal(applyDraftImport(model,base,rows),true);assert.equal(model.state.dirty,true);assert.equal(model.state.data,base.data);assert.deepEqual(model.state.entries,rows);
 rows[0].title='changed outside';assert.equal(model.state.entries[0].title,'标题');assert.equal(applyDraftImport(model,base,[]),false);
});
test('dirty, busy, blocked, checking, read-only, logout and changed baselines deny import',()=>{
 for(const patch of [{dirty:true},{busy:true},{blocked:true},{checking:true},{user:null},{user:{version:'session-a',permissions:['knowledge.read']}},{user:{version:'session-a',permissions:['knowledge.edit']}}]){const m=rig(),base=m.state;m.set(patch);assert.equal(canImport(m.state),false);assert.equal(applyDraftImport(m,base,[]),false);}
 for(const alter of [m=>m.clear(),m=>m.set({user:{...m.state.user,version:'session-b'}}),m=>m.set({entries:[entry('new')]}),m=>m.set({data:{...m.state.data,revision:6}})]){const m=rig(),base=m.state;alter(m);assert.equal(importStillCurrent(m.state,base),false);assert.equal(applyDraftImport(m,base,[]),false);}
});
test('late file read cannot overwrite edits made while it was reading',async()=>{
 let finish;const m=rig(),base=m.state,read=readDraftFile({size:2,arrayBuffer:()=>new Promise(resolve=>finish=resolve)});
 m.edit([entry('unsaved')]);finish(new TextEncoder().encode('[]').buffer);const rows=await read;
 assert.equal(applyDraftImport(m,base,rows),false);assert.equal(m.state.entries[0].id,'unsaved');
});
test('exports require readable saved state, never publish permission',()=>{
 const m=rig();assert.ok(exportSavedDraft(m.state));m.set({user:{version:'s',permissions:['knowledge.read']}});assert.ok(exportSavedDraft(m.state));
 for(const patch of [{blocked:true},{busy:true},{checking:true},{user:null},{data:{draft:null}}])assert.equal(exportSavedDraft({...m.state,...patch}),null);
});
