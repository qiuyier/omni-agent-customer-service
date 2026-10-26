// Explicit one-time import, never part of normal start. No secrets printed.
import {readFile,writeFile} from 'node:fs/promises';
import {parseEnv} from 'node:util';
import {fileURLToPath} from 'node:url';
import {aiEnvironment} from './ai.mjs';
try{
 if(process.argv.length!==3||process.argv[2]!=='--from-primary-env')throw Error();
 const root=new URL('../../',import.meta.url),env=parseEnv(await readFile(new URL('.env',root),'utf8'));
 const c={provider:env.AI_PROVIDER,protocol:env.AI_PROTOCOL,model:env.AI_MODEL,baseURL:env.AI_BASE_URL,apiKey:env.AI_API_KEY,faqPath:fileURLToPath(new URL('integrations/standalone-host/ai-human-faq.json',root))};
 aiEnvironment(c);
 await writeFile(new URL('.local/reference/ai.json',root),JSON.stringify(c,null,2)+'\n',{mode:0o600,flag:'wx'});
 console.log('Private AI configuration prepared with fictional FAQ. Existing files are never overwritten. No external requests made.');
}catch{console.error('AI preparation refused: check explicit flag, primary model fields and existing private file. No secrets displayed.');process.exitCode=1;}
