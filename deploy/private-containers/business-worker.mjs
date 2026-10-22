import {startEdge} from './edge.mjs';
import {readFileSync} from 'node:fs';
import {startClient} from './transport.mjs';
import {startBusiness} from '../../integrations/fresh-onboarding/business.mjs';
import {attachSupport} from '../../integrations/fresh-onboarding/support.mjs';
const settings='/run/business/settings.json';
let c;
for(let i=0;i<300;i++){c=JSON.parse(readFileSync(settings));if(c.origin)break;await new Promise(r=>setTimeout(r,100));}
const publicURL=new URL(c.origin);
if(publicURL.hostname!=='127.0.0.1'||publicURL.protocol!=='http:'||publicURL.origin!==c.origin)throw Error('local_test_origin_required');
const client=await startClient({origin:'https://gateway:8443',ca:'/run/business/ca.crt',cert:'/run/business/client.crt',key:'/run/business/client.key'});
const business=await startBusiness({accounts:c.accounts,serveUI:c.serveUI===true});
const support=await attachSupport(business,{gatewayOrigin:'http://127.0.0.1:8094',hostSecret:c.hostSecret,agents:c.agents,storage:'postgres'});
const edge=await startEdge({origin:c.origin,upstreamOrigin:business.origin,serveUI:c.serveUI===true,audit:c.serveUI?(entry=>console.log(JSON.stringify({edge_request:entry}))):undefined});
console.log('business-ready');
for(const signal of ['SIGINT','SIGTERM'])process.once(signal,async()=>{await edge.close();await business.close();await support.close();await client.close();});
