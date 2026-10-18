import test from 'node:test';
import assert from 'node:assert/strict';
import {canReadDuringSend} from './handoff-read.mjs';
test('only confirmed handoff states allow polling during a send',()=>{
 for(const status of ['waiting_human','human_active','closed'])assert.equal(canReadDuringSend(true,status),true);
 for(const status of ['bot_active','',undefined,'unknown'])assert.equal(canReadDuringSend(true,status),false);
 for(const status of ['waiting_human','human_active','closed'])assert.equal(canReadDuringSend(false,status),false);
});
