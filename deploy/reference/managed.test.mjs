import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,writeFile,chmod,rm,symlink} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {aiEnvironment,loadAI} from './ai.mjs';
import {environment,newConfig} from './config.mjs';
const config={provider:'custom',protocol:'chat_completions',model:'offline',baseURL:'https://model.invalid/v1',apiKey:'fictional-only'};
test('question capture defaults off and requires managed explicit boolean',()=>{
 assert.equal(aiEnvironment(config,{managed:true}).WEBCHAT_KNOWLEDGE_FEEDBACK,undefined);
 assert.equal(aiEnvironment({...config,captureUnmatched:true},{managed:true}).WEBCHAT_KNOWLEDGE_FEEDBACK,'true');
 assert.equal(aiEnvironment({...config,captureUnmatched:false},{managed:true}).WEBCHAT_KNOWLEDGE_FEEDBACK,undefined);
 assert.throws(()=>aiEnvironment({...config,captureUnmatched:'true'},{managed:true}));
 assert.throws(()=>aiEnvironment({...config,faqPath:'/tmp/faq.json',captureUnmatched:true}));
});
test('managed knowledge is explicit and excludes file source; default remains fake',()=>{
 const env=aiEnvironment(config,{managed:true});assert.equal(env.WEBCHAT_KNOWLEDGE_SOURCE,'managed');assert.equal(env.KNOWLEDGE_FAQ_PATH,undefined);assert.equal(env.WEBCHAT_REPLY_MODE,'ai');assert.equal(env.AI_ALLOW_INSECURE_HTTP,'false');
 assert.equal(environment(newConfig()).WEBCHAT_KNOWLEDGE_SOURCE,undefined);
 assert.throws(()=>aiEnvironment(config));
 for(const change of [{faqPath:'/tmp/a.json'},{apiKey:''},{baseURL:'http://127.0.0.1:9000'},{protocol:'responses'},{extra:true}])assert.throws(()=>aiEnvironment({...config,...change},{managed:true}));
});
test('managed config is separate, private and never falls back to file AI',async t=>{
 const dir=await mkdtemp(join(tmpdir(),'omni-managed-config-'));t.after(()=>rm(dir,{recursive:true,force:true}));
 await writeFile(join(dir,'ai.json'),JSON.stringify({...config,faqPath:'/tmp/faq.json'}),{mode:0o600});
 await assert.rejects(loadAI(dir,{managed:true}));
 const file=join(dir,'ai-managed.json');await writeFile(file,JSON.stringify(config),{mode:0o600});assert.equal((await loadAI(dir,{managed:true})).WEBCHAT_KNOWLEDGE_SOURCE,'managed');
 assert.equal((await loadAI(dir)).KNOWLEDGE_FAQ_PATH,'/tmp/faq.json');
 await chmod(file,0o644);await assert.rejects(loadAI(dir,{managed:true}));await rm(file);await symlink(join(dir,'ai.json'),file);await assert.rejects(loadAI(dir,{managed:true}));
});
