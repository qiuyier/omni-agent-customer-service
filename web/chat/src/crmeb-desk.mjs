import {APIError,requestJSON} from './protocol.mjs';

// Original CRMEB staff login cookie; never accept the customer login token.
export function readStaffLogin(cookie=''){
 try {
  const name='from-crmeb-admin:kefu_token';
  const matches=cookie.split(';').map(x=>x.trim().split('=')).filter(([key])=>decodeURIComponent(key)===name);
  if(matches.length!==1)return '';
  const value=decodeURIComponent(matches[0].slice(1).join('='));
  return /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(value)&&value.length<=4096?value:'';
 }catch{return '';}
}

export function createCRMEBDeskAPI({getLogin,fetcher=globalThis.fetch}){
 let pinned='',generation=0;
 return async (path,options={})=>{
  if(!['/desk/me','/desk/queue','/desk/action'].includes(path))throw new APIError('not_found',404);
  const token=getLogin();
  if(path==='/desk/me'&&token!==pinned){pinned=token;generation++;}
  if(!token||token!==pinned)throw new APIError('account_changed',401);
  const started=generation;
  const check=()=>{if(started!==generation||getLogin()!==token)throw new APIError('account_changed',401);};
  const wrapped=(url,init)=>fetcher(url,{...init,headers:{...init.headers,'Authori-zation':'Bearer '+token},redirect:'error'});
  try{const result=await requestJSON('/api/omni-chat'+path,{...options,fetcher:wrapped});check();return result;}
  catch(e){check();throw e;}
 };
}
