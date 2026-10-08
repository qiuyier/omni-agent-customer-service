// Local protocol fixture, NOT an AI model or production host authentication.
import http from 'node:http';
import { randomBytes } from 'node:crypto';
import { mkdtemp, rm, readFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';

const root = fileURLToPath(new URL('../../', import.meta.url));
const faq = fileURLToPath(new URL('../../examples/knowledge/device-repair/faq.json', import.meta.url));
export async function startDemo({forward, modelName = 'local-repair-fixture', faqPath = faq} = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'omni-repair-'));
  const executable = join(directory, 'webchat-demo');
  const key = randomBytes(24).toString('hex');
  const calls = [], failures = [];
  let child, server, exitPromise;
  let stopped = false;
  const stop = async () => {
    if (stopped) return;
    stopped = true;
    if (child && child.exitCode === null && child.signalCode === null) {
      child.kill('SIGTERM');
      const timeout = setTimeout(() => child.kill('SIGKILL'), 8000);
      try { await exitPromise; } finally { clearTimeout(timeout); }
    }
    if (server?.listening) {
      server.closeAllConnections();
      await new Promise(resolve => server.close(resolve));
    }
    await rm(directory, {recursive:true, force:true});
  };
  try {
    const allowedIDs = new Set(JSON.parse(await readFile(faqPath, 'utf8')).map(s => s.id));
    await promisify(execFile)('go', ['build', '-o', executable, './cmd/webchat-demo'], {cwd:root, timeout:120000});
    server = http.createServer(async (req, res) => {
      try {
        if (req.method !== 'POST' || req.url !== '/v1/chat/completions' || req.headers.authorization !== `Bearer ${key}`) {
          res.writeHead(403).end(); return;
        }
        let body = '';
        for await (const chunk of req) { body += chunk; if (Buffer.byteLength(body) > 1024*1024) throw Error('too large'); }
        const payload = JSON.parse(body);
        if (payload.stream) throw Error('stream not implemented by fixture');
        const messages = payload.messages.map(m => ({...m, content: Array.isArray(m.content) ? m.content.map(part => part.text || '').join('') : m.content}));
        const envelope = messages.find(m => m.role === 'user' && m.content?.startsWith('BEGIN_UNTRUSTED_KNOWLEDGE_JSON\n'));
        if (!envelope) throw Error('missing reference envelope');
        const references = JSON.parse(envelope.content.split('\n')[1]);
        if (references.kind !== 'untrusted_knowledge_references' || !Array.isArray(references.snippets)) throw Error('invalid envelope');
        if (references.snippets.some(s => !allowedIDs.has(s.id))) throw Error('foreign knowledge');
        calls.push(messages);
        if (forward) {
          const result = await forward(payload);
          res.writeHead(200, {'Content-Type':'application/json'});
          res.end(JSON.stringify(result));
          return;
        }
        const content = '【本地模型替身：只验证接入，不代表 AI 质量】' + (references.snippets.length
          ? references.snippets.map(s => s.content).join('\n')
          : '当前资料无法确认，且没有实时查询或代办工具。');
        res.writeHead(200, {'Content-Type':'application/json'});
        res.end(JSON.stringify({id:'repair-fixture', object:'chat.completion', created:1, model:'local-repair-fixture', choices:[{index:0,message:{role:'assistant',content},finish_reason:'stop'}]}));
      } catch (error) { failures.push(error.message); res.writeHead(400).end(); }
    });
    await new Promise((resolve,reject) => { server.once('error',reject); server.listen(0,'127.0.0.1',resolve); });
    // Do not load .env, CRMEB credentials, databases or provider configuration.
    const env = {PATH:process.env.PATH, AI_PROVIDER:'custom', AI_PROTOCOL:'chat_completions', AI_MODEL:modelName, AI_API_KEY:key,
      AI_BASE_URL:`http://127.0.0.1:${server.address().port}/v1`, KNOWLEDGE_FAQ_PATH:faqPath, KNOWLEDGE_RETRIEVER:'keyword'};
    child = spawn(executable, ['-address','127.0.0.1:0','-storage','memory','-reply','ai'], {cwd:root,env,stdio:['ignore','pipe','pipe']});
    exitPromise = new Promise(resolve => child.once('close',resolve));
    // Startup diagnostics are deliberately not copied into reports or the browser.
    child.stderr.resume();
    const origin = await new Promise((resolve,reject) => {
      const timer = setTimeout(() => reject(Error('demo startup timeout')),15000);
      let output='';
      child.once('error', () => {clearTimeout(timer);reject(Error('cannot start demo'));});
      child.once('exit', () => {clearTimeout(timer);reject(Error('demo exited before startup'));});
      child.stdout.on('data', data => {
        output += data;
        const match = output.match(/Web chat demo: (http:\/\/127\.0\.0\.1:\d+)\n/);
        if (match) {clearTimeout(timer);resolve(match[1]);}
      });
    });
    return {origin,calls,failures,stop};
  } catch(error) {await stop();throw error;}
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const demo = await startDemo();
  console.log(`设备维修接入样例：${demo.origin}\n复用模拟账号林澈/周宁；本地模型替身，无外部模型调用；内存历史在退出后清空。\n示例问题：如何报修？受理时间是几点？维修费用是多少？`);
  for (const signal of ['SIGINT','SIGTERM']) process.once(signal, async () => {await demo.stop();});
}
