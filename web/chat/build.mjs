import { build } from 'esbuild';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('.', import.meta.url));
const output = fileURLToPath(new URL('../../internal/webchat/ui/assets/', import.meta.url));
await build({
  entryPoints: [new URL('src/app.jsx', import.meta.url).pathname],
  outfile: `${output}app.js`,
  bundle: true,
  minify: true,
  sourcemap: false,
  legalComments: 'linked',
  target: ['es2022'],
  define: { 'process.env.NODE_ENV': '"production"' },
});
execFileSync(`${root}node_modules/.bin/tailwindcss`, [
  '-i', `${root}src/styles.css`, '-o', `${output}app.css`, '--minify',
], { cwd: root, stdio: 'inherit' });
