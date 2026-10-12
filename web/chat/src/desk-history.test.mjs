import test from 'node:test';
import assert from 'node:assert/strict';
import {mergeDeskHistory,deskTailCursor} from './desk-history.mjs';
import {readDeskSnapshot} from './polling.mjs';
const page=(ids,extra={})=>({conversation_id:'c',messages:ids.map(id=>({id,content:id})),unread_message_ids:ids,has_older:true,has_newer:false,...extra});
test('prepend and append preserve append order even when IDs/timestamps do not sort',()=>{
 let state=page(['z','a']);state=mergeDeskHistory(state,page(['b','z'],{has_older:false}),'before');
 assert.deepEqual(state.messages.map(m=>m.id),['b','z','a']);assert.equal(state.has_older,false);
 state=mergeDeskHistory(state,page(['a','f']));assert.deepEqual(state.messages.map(m=>m.id),['b','z','a','f']);
 assert.equal(state.has_older,false);assert.deepEqual(deskTailCursor(state),{after_message_id:'f'});
});
test('empty incremental page preserves loaded history and old unread, refreshes metadata',()=>{
 const state=mergeDeskHistory(page(['old']),page([],{unread_count:7,first_unread_id:'outside'}));
 assert.deepEqual(state.messages.map(m=>m.id),['old']);assert.deepEqual(state.unread_message_ids,['old']);assert.equal(state.unread_count,7);
});
test('page receipt state replaces only IDs covered by that page',()=>{
 const state=mergeDeskHistory(page(['one','two']),page(['two','three'],{unread_message_ids:['three']}));
 assert.deepEqual(state.unread_message_ids,['one','three']);
});
test('different conversation never merges cached pages',()=>{
 const other={...page(['new']),conversation_id:'other'};assert.deepEqual(mergeDeskHistory(page(['old']),other),other);
});
test('incremental polling forwards exact last ID and remains a read, across a >100 backlog',async()=>{
 const calls=[];const request=async(path,options)=>{calls.push([path,options]);return path==='/desk/me'?{agent:{id:'a'}}:path==='/desk/queue'?{items:[]}:{...page(['next']),has_newer:true};};
 const result=await readDeskSnapshot(request,'a','c',null,{after_message_id:'last'});
 assert.deepEqual(calls.find(([p])=>p==='/desk/action')[1].body,{after_message_id:'last',conversation_id:'c',action:'history'});
 assert.equal(result.selected.has_newer,true);
});
