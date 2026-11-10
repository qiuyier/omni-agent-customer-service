import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const entry=fileURLToPath(new URL('./knowledge_feedback_flow_check.mjs',import.meta.url));
test('feedback flow help does not create resources',()=>{const r=spawnSync(process.execPath,[entry,'--help'],{encoding:'utf8'});assert.equal(r.status,0);assert.match(r.stdout,/Local mock model only/);assert.doesNotMatch(r.stdout,/Report directory/);});
test('feedback flow refuses writes without exact explicit flag',()=>{for(const args of [[],['--postgres'],['--allow-test-writes','--help'],['--allow-test-writes','--allow-test-writes'],['--dsn=postgres://existing']]){const r=spawnSync(process.execPath,[entry,...args],{encoding:'utf8'});assert.equal(r.status,2);assert.doesNotMatch(r.stdout,/Report directory/);}});
