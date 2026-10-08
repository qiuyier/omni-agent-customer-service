// Generates private local deployment material; never prints credentials.
import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import { fileURLToPath } from 'node:url';
const dir = path.dirname(fileURLToPath(import.meta.url));
const crmeb = '/Users/LOCAL_USER/Code/DockerProject/dnmp8/www/CRMEB/crmeb';
const privateDir = path.join(crmeb,'runtime/omni-chat-deploy');
const target = path.join(privateDir,'config.json'), env = path.join(dir,'.env');
if (!fs.existsSync(path.join(crmeb,'app/api/controller/OmniChat.php'))) throw Error('install bridge first');
if (fs.existsSync(target) || fs.existsSync(env)) throw Error('private configuration already exists; preserve it and reuse the deployment');
fs.mkdirSync(privateDir,{mode:0o700});
const password = crypto.randomBytes(24).toString('hex'), secret = crypto.randomBytes(24).toString('hex');
fs.writeFileSync(env,`OMNI_PG_PASSWORD=${password}\nOMNI_HOST_SECRET=${secret}\n`,{mode:0o600,flag:'wx'});
fs.writeFileSync(target,JSON.stringify({enabled:true,origin:'http://demo.crmeb.com',upstream:'http://127.0.0.1:8093',secret,allow_local_http:true,storage:'postgres',reply_mode:'fake'},null,2),{mode:0o600,flag:'wx'});
console.log('Private configuration generated. No credentials printed. Enable bridge only after migration and gateway readiness.');
