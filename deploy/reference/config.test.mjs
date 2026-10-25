import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,rm,chmod,readFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {newConfig,validate,environment,initialize,load} from './config.mjs';
import net from 'node:net';
import {assertAvailable} from './ports.mjs';
test('fixed identity, private independent credentials and fake-only clean environment',()=>{
 const a=newConfig(),b=newConfig();assert.equal(a.hostID,b.hostID);assert.notEqual(a.hostSecret,b.hostSecret);assert.equal(validate(a),a);
 const env=environment(a);assert.equal(env.WEBCHAT_REPLY_MODE,'fake');assert.equal(env.AI_API_KEY,undefined);assert.equal(env.NODE_OPTIONS,undefined);
 assert.throws(()=>validate({...a,hostID:'other'}));assert.throws(()=>validate({...a,agentSecret:a.hostSecret}));assert.throws(()=>validate({...a,replyMode:'ai'}));
});
test('occupied loopback port is refused without touching existing service',async t=>{
 const server=net.createServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));t.after(()=>new Promise(resolve=>server.close(resolve)));
 await assert.rejects(assertAvailable(server.address().port));assert.equal(server.listening,true);
});
test('initialization refuses overwrite; restart reads unchanged configuration; unsafe permissions rejected',async t=>{
 const root=await mkdtemp(join(tmpdir(),'omni-reference-config-'));t.after(()=>rm(root,{recursive:true,force:true}));const directory=join(root,'private');
 const first=await initialize(directory);assert.deepEqual(await load(directory),first);await assert.rejects(initialize(directory));assert.deepEqual(await load(directory),first);
 const file=join(directory,'config.json');assert.equal(JSON.parse(await readFile(file)).hostSecret,first.hostSecret);
 await chmod(file,0o644);await assert.rejects(load(directory));
});
