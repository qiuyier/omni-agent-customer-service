// Explicitly opt into local-only account selection; never use this as production auth.
import {startAdapter} from '../host-kit/adapter.mjs';
export function startHost(options){return startAdapter({...options,demoAccounts:{lin:{id:'member-1001',display_name:'林澈'},zhou:{id:'member-1002',display_name:'周宁'}}});}
