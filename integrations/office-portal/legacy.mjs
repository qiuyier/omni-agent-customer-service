// A deliberately separate, local-only stand-in for an existing office IAM service.
// Its nested records and member/operator vocabulary are not the support protocol.
import {createHash,randomBytes,scrypt,timingSafeEqual} from 'node:crypto';
import {promisify} from 'node:util';

const derive=promisify(scrypt);
const hash=value=>createHash('sha256').update(value).digest('hex');
const realms=new Set(['member','operator']);
const lifetime=30*60*1000;
export async function createLegacyDirectory({accounts,now=Date.now}){
 if(!Array.isArray(accounts)||accounts.length<1||accounts.length>32)throw Error('invalid office directory configuration');
 const directory=new Map(),numbers=new Set(),tickets=new Map();
 for(const entry of accounts){
  if(!entry||!realms.has(entry.realm)||typeof entry.handle!=='string'||!/^[A-Za-z0-9_-]{1,64}$/.test(entry.handle)||typeof entry.passphrase!=='string'||entry.passphrase.length<16||entry.passphrase.length>512||typeof entry.number!=='string'||!(entry.realm==='member'?/^MEM:[A-Za-z0-9_-]{1,80}$/:/^OPS:[A-Za-z0-9_-]{1,80}$/).test(entry.number)||(entry.active!==undefined&&typeof entry.active!=='boolean'))throw Error('invalid office directory configuration');
  const key=entry.realm+':'+entry.handle;
  if(directory.has(key)||numbers.has(entry.number))throw Error('duplicate office account');
  const salt=randomBytes(16);
  directory.set(key,{principal:{number:entry.number,realm:entry.realm},lifecycle:{active:entry.active!==false},salt,proof:await derive(entry.passphrase,salt,32)});
  numbers.add(entry.number);
 }
 // Perform the same password derivation for unknown handles as known ones.
 const dummySalt=randomBytes(16),dummyProof=await derive(randomBytes(32).toString('hex'),dummySalt,32);
 let attempts=0,windowStart=now();
 const cookieName=realm=>'office_'+realm;
 function presented(request,realm){
  if(!realms.has(realm))return null;
  const entries=String(request.headers.cookie||'').split(';').map(value=>value.trim()).filter(value=>value.startsWith(cookieName(realm)+'='));
  if(entries.length!==1)return null;
  const raw=entries[0].slice(cookieName(realm).length+1);
  if(!/^[A-Za-z0-9_-]{43}$/.test(raw))return null;
  const key=hash(raw),ticket=tickets.get(key);
  // A renamed cookie must never revoke a valid ticket in the other realm.
  if(!ticket||ticket.realm!==realm)return null;
  const account=directory.get(ticket.account);
  if(!account?.lifecycle.active||ticket.deadline<=now()){tickets.delete(key);return null;}
  return {principal:{...account.principal},lifecycle:{...account.lifecycle},ticket:{key,deadline:ticket.deadline,publicRevision:ticket.publicRevision}};
 }
 async function open(request,realm,handle,passphrase){
  if(!realms.has(realm)||typeof handle!=='string'||!/^[A-Za-z0-9_-]{1,64}$/.test(handle)||typeof passphrase!=='string'||passphrase.length>512)return null;
  if(now()-windowStart>=3600000){attempts=0;windowStart=now();}
  if(++attempts>128)throw Object.assign(Error('login limited'),{limit:true});
  const account=directory.get(realm+':'+handle);
  const candidate=await derive(passphrase,account?.salt||dummySalt,32);
  if(!timingSafeEqual(candidate,account?.proof||dummyProof)||!account?.lifecycle.active)return null;
  for(const [key,ticket] of tickets)if(ticket.deadline<=now())tickets.delete(key);
  const previous=presented(request,realm);
  if(tickets.size-(previous?1:0)>=128)throw Object.assign(Error('login limited'),{limit:true});
  if(previous)tickets.delete(previous.ticket.key);
  const raw=randomBytes(32).toString('base64url');
  tickets.set(hash(raw),{account:realm+':'+handle,realm,deadline:now()+lifetime,publicRevision:randomBytes(24).toString('base64url')});
  return `${cookieName(realm)}=${raw}; Path=/; HttpOnly; SameSite=Strict; Max-Age=${lifetime/1000}`;
 }
 function close(request,realm){
  if(!realms.has(realm))throw Error('invalid realm');
  const current=presented(request,realm);if(current)tickets.delete(current.ticket.key);
  return `${cookieName(realm)}=; Path=/; HttpOnly; SameSite=Strict; Max-Age=0`;
 }
 return {
  authenticate:presented,open,close,
  setActive(realm,handle,active){
   const key=realm+':'+handle,account=directory.get(key);
   if(!account||typeof active!=='boolean')throw Error('invalid test directory change');
   account.lifecycle.active=active;
   if(!active)for(const [ticketKey,ticket] of tickets)if(ticket.account===key)tickets.delete(ticketKey);
  },
  dispose(){tickets.clear();directory.clear();},
 };
}
