import test from 'node:test';
import assert from 'node:assert/strict';
import {beginTrace,failTrace,recordAction} from './receipt-trace.mjs';
test('a failure before upstream headers still records the original claim and stage',()=>{
 const e=beginTrace(1,'POST','/api/omni-chat/desk/action',0);recordAction(e,'{"action":"claim","revision":2,"token":"secret"}');e.stage='upstream_fetch';
 failTrace(e,{name:'TypeError',message:'password=secret',cause:{code:'UND_ERR_SOCKET',socket:{secret:'private'}}},15);
 assert.equal(e.action,'claim');assert.equal(e.failed_stage,'upstream_fetch');assert.equal(e.error_code,'UND_ERR_SOCKET');assert.equal(e.duration_ms,15);assert.ok(!JSON.stringify(e).includes('secret'));
});
test('upstream success followed by decoding failure remains distinguishable',()=>{
 const e=beginTrace(2,'POST','/api/omni-chat/desk/action',0);e.status=200;e.stage='upstream_body';failTrace(e,{name:'SyntaxError'},10);
 assert.equal(e.status,200);assert.equal(e.failed_stage,'upstream_body');
});
test('unknown errors and forged action values cannot leak arbitrary text',()=>{
 const e=beginTrace(3,'POST','/api/omni-chat/desk/action',0);recordAction(e,'{"action":"token-secret","revision":"secret"}');failTrace(e,{name:'secret',code:'secret',stack:'secret'},20);
 assert.equal(e.action,undefined);assert.equal(e.error_code,'OTHER');assert.equal(e.error_name,'OtherError');assert.ok(!JSON.stringify(e).includes('secret'));
});
