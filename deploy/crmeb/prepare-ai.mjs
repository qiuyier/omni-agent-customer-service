// Explicitly import only the primary .env model fields, never .env.local or DB credentials.
import fs from 'node:fs';
import {parseEnv} from 'node:util';
import {fileURLToPath} from 'node:url';
const source = parseEnv(fs.readFileSync(new URL('../../.env',import.meta.url),'utf8'));
const keys=['AI_PROVIDER','AI_PROTOCOL','AI_MODEL','AI_BASE_URL','AI_API_KEY','AI_MAX_CONCURRENT_REPLIES','AI_SYSTEM_PROMPT'];
const picked={};
for(const key of keys){const value=source[key]??'';if(/[\r\n\0]/.test(value))throw Error('Multiline model settings require explicit review');if(value)picked[key]=value;}
for(const key of ['AI_PROVIDER','AI_PROTOCOL','AI_MODEL','AI_BASE_URL','AI_API_KEY'])if(!picked[key])throw Error('Missing primary model setting: '+key);
const url=new URL(picked.AI_BASE_URL);if(url.protocol!=='https:'||url.username||url.password||url.search||url.hash)throw Error('A credential-free HTTPS model endpoint is required');
const target=fileURLToPath(new URL('.ai.env',import.meta.url));
fs.writeFileSync(target,Object.entries(picked).map(([k,v])=>k+'='+v).join('\n')+'\n',{mode:0o600,flag:'wx'});
console.log('Private AI environment prepared from primary .env; no credentials displayed. Explicit AI compose override required.');
