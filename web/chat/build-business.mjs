import {build} from 'esbuild';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
const root=fileURLToPath(new URL('.',import.meta.url));
const output=fileURLToPath(new URL('../../integrations/business-login/public/',import.meta.url));
await build({entryPoints:[root+'src/business.jsx'],outfile:output+'app.js',bundle:true,minify:true,sourcemap:false,legalComments:'linked',target:['es2022'],define:{'process.env.NODE_ENV':'"production"'}});
execFileSync(root+'node_modules/.bin/tailwindcss',['-i',root+'src/business.css','-o',output+'app.css','--minify'],{cwd:root,stdio:'inherit'});
