import {browserGate,responseLoss} from './browser-gate.mjs';
import {reloadable} from './reloadable.mjs';
import {createSecureContext} from 'node:tls';
import {validateSupportConfig} from '../../integrations/host-kit/onboarding.mjs';
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
const business=await startBusiness({accounts:c.accounts,serveUI:c.serveUI===true});
const support=reloadable({readConfig:()=>JSON.parse(readFileSync(settings)),validate:next=>{
 if(next.origin!==c.origin||JSON.stringify(next.accounts)!==JSON.stringify(c.accounts)||next.serveUI!==c.serveUI)throw Error('business_configuration_changed');
 validateSupportConfig({gatewayOrigin:'http://127.0.0.1:8094',hostSecret:next.hostSecret,agents:next.agents,storage:'postgres',replyMode:'fake'});
 createSecureContext({ca:readFileSync('/run/business/ca.crt'),cert:readFileSync('/run/business/client.crt'),key:readFileSync('/run/business/client.key')});
},start:async next=>{
 const client=await startClient({origin:'https://gateway:8443',ca:'/run/business/ca.crt',cert:'/run/business/client.crt',key:'/run/business/client.key'});
 try{let handle;const attached=await attachSupport({...business,mount:h=>{handle=h;}},{gatewayOrigin:'http://127.0.0.1:8094',hostSecret:next.hostSecret,agents:next.agents,storage:'postgres'});return {handle,close:async()=>{await attached.close();await client.close();}};}catch(e){await client.close();throw e;}
}});
business.mount((...args)=>support.handle(...args));await support.reload();
process.on('SIGUSR2',()=>support.reload().then(()=>console.log('support-reloaded')).catch(()=>console.log('support-reload-failed')));
const gate=c.serveUI?browserGate({readMode:()=>{try{return readFileSync('/run/business/browser-gate','utf8').trim();}catch{return '';}},log:entry=>console.log(JSON.stringify({browser_gate:entry}))}):undefined;
const loss=c.serveUI?responseLoss({readMode:()=>{try{return readFileSync('/run/business/browser-gate','utf8').trim();}catch{return '';}},log:entry=>console.log(JSON.stringify({browser_gate:entry}))}):undefined;
const edge=await startEdge({afterResponse:loss,beforeForward:gate,origin:c.origin,upstreamOrigin:business.origin,serveUI:c.serveUI===true,audit:c.serveUI?(entry=>console.log(JSON.stringify({edge_request:entry}))):undefined});
console.log('business-ready');
for(const signal of ['SIGINT','SIGTERM'])process.once(signal,async()=>{await edge.close();await business.close();await support.close();});
