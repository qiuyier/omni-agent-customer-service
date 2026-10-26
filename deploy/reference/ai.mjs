import {lstat,readFile} from 'node:fs/promises';
import {isAbsolute,join} from 'node:path';
export function aiEnvironment(c){
 if(!c||Object.keys(c).sort().join(',')!=='apiKey,baseURL,faqPath,model,protocol,provider'||Object.values(c).some(v=>typeof v!=='string'||!v.trim()||/[\r\n\0]/.test(v)))throw Error('invalid private AI configuration');
 if(c.protocol!=='chat_completions'||!isAbsolute(c.faqPath))throw Error('explicit protocol and absolute FAQ path required');
 const url=new URL(c.baseURL);
 if(url.protocol!=='https:'||url.username||url.password||url.search||url.hash||/\/(chat\/completions|responses)\/?$/.test(url.pathname))throw Error('clean HTTPS API root required');
 return {WEBCHAT_REPLY_MODE:'ai',AI_PROVIDER:c.provider,AI_PROTOCOL:c.protocol,AI_MODEL:c.model,AI_BASE_URL:c.baseURL,AI_API_KEY:c.apiKey,AI_ALLOW_INSECURE_HTTP:'false',KNOWLEDGE_RETRIEVER:'keyword',KNOWLEDGE_FAQ_PATH:c.faqPath,KNOWLEDGE_RETRIEVAL_TIMEOUT:'2s'};
}
export async function loadAI(directory){
 const file=join(directory,'ai.json'),stat=await lstat(file);
 if(!stat.isFile()||stat.isSymbolicLink()||(stat.mode&0o077)||stat.size>16384)throw Error('unsafe AI configuration file');
 return aiEnvironment(JSON.parse(await readFile(file,'utf8')));
}
