import test from 'node:test';
import assert from 'node:assert/strict';
import {reloadable} from './reloadable.mjs';
test('invalid configuration keeps active generation; failed start closes access; recovery is explicit',async()=>{
 let config=1,closed=[],starts=[],fail=false;
 const r=reloadable({readConfig:()=>config,validate:c=>{if(c<0)throw Error('invalid');},start:async c=>{starts.push(c);if(fail)throw Error('start failed');return {handle:()=>c,close:()=>closed.push(c)};}});
 await r.reload();assert.equal(await r.handle(),1);
 config=-1;await assert.rejects(r.reload());assert.equal(await r.handle(),1);assert.deepEqual(closed,[]);
 config=2;fail=true;await assert.rejects(r.reload());let status;await r.handle({}, {writeHead:s=>{status=s;return {end(){}};}});assert.equal(status,503);assert.deepEqual(closed,[1]);
 fail=false;await r.reload();assert.equal(await r.handle(),2);await r.close();await assert.rejects(r.reload());assert.deepEqual(starts,[1,2,2]);
});
test('reload requests serialize and close each replaced generation once',async()=>{
 let generation=0,closed=[];const r=reloadable({readConfig:()=>++generation,validate:()=>{},start:async c=>({handle:()=>c,close:()=>closed.push(c)})});
 await Promise.all([r.reload(),r.reload(),r.reload()]);assert.equal(await r.handle(),3);assert.deepEqual(closed,[1,2]);await r.close();await r.close();assert.deepEqual(closed,[1,2,3]);
});
