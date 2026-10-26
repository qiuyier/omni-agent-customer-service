// Only business identity and configuration binding; shared assembly stays in host-kit.
import {mountSupport} from '../host-kit/onboarding.mjs';
export function attachSupport(business,{gatewayOrigin,hostSecret,agents,storage='memory',replyMode='fake'}){
 return mountSupport({config:{gatewayOrigin,hostSecret,agents,storage,replyMode},sessions:business.sessions,publicOrigin:()=>business.origin,mount:handler=>business.mount(handler)});
}
