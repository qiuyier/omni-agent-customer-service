import test from 'node:test';
import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const entry=fileURLToPath(new URL('./schema_upgrade_check.mjs',import.meta.url));
test('upgrade check help does not create resources',()=>{
 const r=spawnSync(process.execPath,[entry,'--help'],{encoding:'utf8'});assert.equal(r.status,0);assert.match(r.stdout,/dedicated PostgreSQL/);assert.doesNotMatch(r.stdout,/Report directory/);
});
test('upgrade writes require exact explicit option before any resources',()=>{
 for(const args of [[],['--postgres'],['--allow-test-writes','--help'],['--allow-test-writes','--allow-test-writes'],['--dsn=postgres://invalid']]){
  const r=spawnSync(process.execPath,[entry,...args],{encoding:'utf8'});assert.equal(r.status,2);assert.doesNotMatch(r.stdout,/Report directory/);
 }
});
