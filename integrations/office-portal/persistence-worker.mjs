// Private child of persistence-browser-lab; no environment-file loading.
import {startPortal} from './portal.mjs';
const portal=await startPortal({...JSON.parse(process.env.OFFICE_TEST_CONFIG),storage:'postgres',serveUI:true});
console.log(portal.origin);
for(const signal of ['SIGTERM','SIGINT'])process.once(signal,async()=>{await portal.close();});
