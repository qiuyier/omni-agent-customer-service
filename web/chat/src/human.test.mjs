import test from 'node:test';
import assert from 'node:assert/strict';
import {createChatAPI} from './protocol.mjs';
import {demoPresentation} from './demo.mjs';
test('human capability is explicit and handoff cannot choose a conversation or agent',async()=>{
 const requests=[];const api=createChatAPI(async(path,options)=>{requests.push({path,options});return new Response(JSON.stringify(path==='/host/chat-session'?{access_token:'test-token',conversation_id:'own',user:{id:'a'},human_enabled:true}:{status:'waiting_human'}),{status:200});});
 assert.equal((await api.open()).human_enabled,true);await api.handoff();assert.equal(requests[1].path,'/chat/v1/handoff');assert.deepEqual(JSON.parse(requests[1].options.body),{});assert.equal(requests[1].options.headers.Authorization,'Bearer test-token');
 assert.match(demoPresentation('postgres','fake',true).capabilities,/可申请人工/);assert.match(demoPresentation('postgres','fake','true').capabilities,/不提供.*人工转接/);
});
