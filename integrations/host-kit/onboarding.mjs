// Repository-local assembly only: no account system, gateway/database startup or migration.
import {startAdapter} from './adapter.mjs';
import {businessIdentity} from './identity.example.mjs';
import {createSupportProxy} from './proxy.mjs';

export function validateSupportConfig(config) {
  const {gatewayOrigin, hostSecret, agents, storage, replyMode} = config || {};
  const u = new URL(gatewayOrigin);
  if (u.protocol !== 'http:' || u.hostname !== '127.0.0.1' || !u.port || u.origin !== gatewayOrigin) throw Error('private_loopback_gateway_required');
  const secret = value => typeof value === 'string' && /^[A-Za-z0-9_-]{32,128}$/.test(value) && !value.startsWith('chat_');
  if (!secret(hostSecret) || !['memory','postgres'].includes(storage) || !['fake','ai'].includes(replyMode) || replyMode==='ai'&&storage!=='postgres') throw Error('invalid_onboarding_configuration');
  if (!Array.isArray(agents) || !agents.length || agents.length > 32 || agents.some(a => !a || typeof a.id !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(a.id) || typeof a.name !== 'string' || !a.name || !secret(a.secret) || a.secret === hostSecret) || new Set(agents.map(a=>a.id)).size !== agents.length || new Set(agents.map(a=>a.secret)).size !== agents.length) throw Error('invalid_authorized_agents');
  return {gatewayOrigin, hostSecret, agents: agents.map(a=>({id:a.id,name:a.name,secret:a.secret})), storage, replyMode};
}

// mount must install the exact handler on the SAME business origin, and reject on
// failure. Unknown routes return false; the business keeps auth and normal routes.
export async function mountSupport({config, sessions, publicOrigin, mount}) {
  const validated = validateSupportConfig(config);
  if (typeof sessions?.authenticate !== 'function' || typeof publicOrigin !== 'function' || typeof mount !== 'function') throw Error('business_binding_required');
  const origin = publicOrigin(), u = new URL(origin);
  if (u.protocol !== 'http:' || u.hostname !== '127.0.0.1' || !u.port || u.origin !== origin) throw Error('business_loopback_origin_required');
  const adapter = await startAdapter({...validated, identity: businessIdentity({sessions})});
  let closing;
  const close = () => closing ??= adapter.close();
  try {
    const handle = createSupportProxy({adapterOrigin: adapter.origin, publicOrigin, sessions,timeoutMs:supportProxyTimeout(validated.replyMode)});
    await mount(handle);
    return {close};
  } catch {
    await close();
    throw Error('support_mount_failed');
  }
}
export function supportProxyTimeout(replyMode){
 if(!['fake','ai'].includes(replyMode))throw Error('invalid reply mode');
 return replyMode==='ai'?125000:20000;
}
