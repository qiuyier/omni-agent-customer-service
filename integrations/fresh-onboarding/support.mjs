// Business identity and lifecycle stay here; HTTP safety lives in host-kit.
import {startAdapter} from '../host-kit/adapter.mjs';
import {businessIdentity} from '../host-kit/identity.example.mjs';
import {createSupportProxy} from '../host-kit/proxy.mjs';
export async function attachSupport(business,{gatewayOrigin,hostSecret,agents}){
 const adapter=await startAdapter({gatewayOrigin,hostSecret,agents,storage:'memory',replyMode:'fake',identity:businessIdentity({sessions:business.sessions})});
 try{business.mount(createSupportProxy({adapterOrigin:adapter.origin,publicOrigin:()=>business.origin,sessions:business.sessions}));}
 catch(e){await adapter.close();throw e;}
 return {close:()=>adapter.close()};
}
