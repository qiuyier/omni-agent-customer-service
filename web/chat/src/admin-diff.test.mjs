import test from 'node:test';
import assert from 'node:assert/strict';
import {publicationDiff} from './admin-diff.mjs';
const row=(id,extra={})=>({id,title:'标题',content:'答案',source:'手册',keywords:['帮助'],...extra});
const state=(before,after)=>({revision:4,active:'old',published:{version:'old',entries:before},draft:{version:'new',entries:after}});
test('diff classifies additions, modifications, removals and unchanged entries without mutation',()=>{
 const data=state([row('keep'),row('edit'),row('remove')],[row('add'),row('edit',{content:'新答案',keywords:['支持']}),row('keep')]);
 const copy=structuredClone(data),diff=publicationDiff(data);
 assert.deepEqual([diff.added,diff.modified,diff.removed],[1,1,1]);
 assert.deepEqual(diff.changes[1].fields,['content','keywords']);assert.equal(diff.changes[1].before.content,'答案');assert.equal(diff.changes[1].after.content,'新答案');assert.deepEqual(data,copy);
});
test('first publication, empty draft and missing published content are distinguished',()=>{
 assert.equal(publicationDiff({active:'',published:null,draft:{entries:[row('a')]}}).added,1);
 const empty=publicationDiff(state([row('a')],[]));assert.equal(empty.empty,true);assert.equal(empty.removed,1);
 assert.equal(publicationDiff({active:'old',draft:{entries:[]}}),null);
 assert.equal(publicationDiff({...state([],[]),published:{version:'wrong',entries:[]}}),null);
 assert.equal(publicationDiff({draft:null}),null);
 assert.equal(publicationDiff(state([],[])).changes.length,0);
});
test('ID changes are remove/add, normalized ID matches, order alone is not a content change',()=>{
 assert.deepEqual(publicationDiff(state([row('a'),row('b')],[row('b'),row('a')])).changes,[]);
 assert.deepEqual(publicationDiff(state([row('a')],[row(' a ')])).changes,[]);
 const d=publicationDiff(state([row('a')],[row('new')]));assert.equal(d.added,1);assert.equal(d.removed,1);
});
test('every field change is visible, including keyword order and literal HTML',()=>{
 const d=publicationDiff(state([row('a')],[row('a',{title:'<script>literal</script>',content:'答案\n',source:'新手册',keywords:['支持','帮助']})]));
 assert.deepEqual(d.changes[0].fields,['title','content','source','keywords']);
});
