import { build } from 'esbuild';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';
import { createHash } from 'node:crypto';
const root = fileURLToPath(new URL('.', import.meta.url));
const out = fileURLToPath(new URL('../../integrations/crmeb/h5/', import.meta.url));
await build({entryPoints: [root+'src/crmeb.jsx'], outfile: out+'app.js', bundle: true, minify: true, sourcemap: false, legalComments: 'linked', target: ['es2022'], define: {'process.env.NODE_ENV':'"production"'}});
execFileSync(root+'node_modules/.bin/tailwindcss', ['-i',root+'src/crmeb.css','-o',out+'app.css','--minify'], {cwd:root,stdio:'inherit'});
// CRMEB can cache JS/CSS aggressively. Version assets by content on each build.
let html = fs.readFileSync(out+'index.html', 'utf8');
for (const name of ['app.js', 'app.css']) {
  const hash = createHash('sha256').update(fs.readFileSync(out+name)).digest('hex').slice(0,16);
  html = html.replace(new RegExp('\\./'+name.replace('.', '\\.')+'(?:\\?v=[a-f0-9]+)?', 'g'), './'+name+'?v='+hash);
}
fs.writeFileSync(out+'index.html', html);
