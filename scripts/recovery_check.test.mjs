import test from 'node:test';
import assert from 'node:assert/strict';
import {parseArgs,cleanEnvironment,assess} from './recovery_check_support.mjs';
test('writes require explicit paired flags; unknown flags fail',()=>{
 assert.equal(parseArgs([]).postgres,false);assert.equal(parseArgs(['--postgres','--allow-test-writes']).postgres,true);
 for(const args of [['--postgres'],['--allow-test-writes'],['--dsn=production'],['--postgres','--postgres']])assert.throws(()=>parseArgs(args));
});
test('deployment and model configuration is not inherited',()=>{
 const env=cleanEnvironment({PATH:'/bin',AI_API_KEY:'secret',DATABASE_URL:'production',TEST_DATABASE_URL:'production_test',NODE_OPTIONS:'inject',GOFLAGS:'unsafe',HOME:'/test'});
 assert.deepEqual(env,{PATH:'/bin',HOME:'/test',GOENV:'off',GOTOOLCHAIN:'local'});
});
test('TAP cannot pass with zero tests, skip, failure, cancellation or nonzero exit',()=>{
 const ok={code:0,text:'# pass 3\n# skipped 0\n# fail 0\n'};assert.equal(assess(ok,'tap').status,'passed');
 for(const r of [{code:0,text:''},{...ok,code:1},{...ok,timedOut:true},{...ok,text:'# pass 3\n# skipped 1'},{...ok,text:'# pass 3\n# cancelled 1'},{...ok,text:'# pass 3\n# fail 1'}])assert.equal(assess(r,'tap').status,'failed');
});
test('Go skips and absent test events are never green',()=>{
 const line=Action=>JSON.stringify({Action,Test:'TestBoundary'});
 assert.equal(assess({code:0,text:line('pass')},'go').status,'passed');
 for(const text of ['',line('skip'),line('pass')+'\n'+line('skip'),line('fail')])assert.equal(assess({code:0,text},'go').status,'failed');
});
