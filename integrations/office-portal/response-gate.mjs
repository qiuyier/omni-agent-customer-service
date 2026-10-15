// Test-only loopback transport. No control endpoint, credentials or response logging.
import http from 'node:http';
export async function startResponseGate(target,{notify=()=>{},holdMs=120000}={}){
 const url=new URL(target);
 if(url.protocol!=='http:'||url.hostname!=='127.0.0.1'||!url.port||url.pathname!=='/'||url.search||url.hash||url.username||url.password)throw Error('loopback_required');
 let origin,armed=false,held=null,closed=false,active=0;
 const requests=new Set();
 const log=event=>{try{notify(event);}catch{}};
 const release=()=>{
  if(!held){armed=false;return false;}
  const item=held;held=null;clearTimeout(item.timer);
  const delivered=!item.response.destroyed;
  if(delivered){item.response.writeHead(item.status,item.headers);item.response.end(item.body);}
  log(delivered?'released':'cancelled_before_release');return delivered;
 };
 const server=http.createServer((request,response)=>{
  const count=name=>request.rawHeaders.filter((v,i)=>i%2===0&&v.toLowerCase()===name).length;
  if(closed||count('host')!==1||request.headers.host!==new URL(origin).host||count('origin')>1||request.headers.origin&&request.headers.origin!==origin||request.headers['sec-fetch-site']==='cross-site'){
   response.writeHead(403,{'Content-Type':'application/json','Cache-Control':'no-store'});response.end('{"code":"request_not_allowed"}');return;
  }
  if(active>=16){response.writeHead(429);response.end();return;}
  active++;let finished=false;
  response.once('close',()=>{if(!finished){finished=true;active--;}});
  const delay=armed&&request.method==='GET'&&request.url==='/chat/v1/messages';
  if(delay)armed=false;
  const headers={...request.headers,host:url.host};
  if(headers.origin)headers.origin=target;
  const upstream=http.request(target+request.url,{method:request.method,headers},incoming=>{
   if(!delay){response.writeHead(incoming.statusCode,incoming.headers);incoming.pipe(response);return;}
   const chunks=[];let size=0;
   incoming.on('data',chunk=>{size+=chunk.length;if(size>1048576){incoming.destroy();response.destroy();return;}chunks.push(chunk);});
   incoming.on('end',()=>{
    if(closed)return;
    if(held){response.destroy();return;}
    held={response,status:incoming.statusCode,headers:incoming.headers,body:Buffer.concat(chunks)};
    held.timer=setTimeout(release,holdMs);held.timer.unref();
    log('captured');
   });
   incoming.on('error',()=>response.destroy());
  });
  requests.add(upstream);upstream.once('close',()=>requests.delete(upstream));
  upstream.setTimeout(20000,()=>upstream.destroy());
  upstream.on('error',()=>{if(!response.headersSent){response.writeHead(502,{'Content-Type':'application/json','Cache-Control':'no-store'});response.end('{"code":"transport_failed"}');}else response.destroy();});
  request.on('aborted',()=>upstream.destroy());request.pipe(upstream);
 });
 await new Promise((resolve,reject)=>{server.once('error',reject);server.listen(0,'127.0.0.1',resolve);});
 origin='http://127.0.0.1:'+server.address().port;
 return {origin,arm(){if(armed||held)throw Error('already_pending');armed=true;log('armed');},release,status:()=>({armed,held:!!held}),async close(){if(closed)return;closed=true;armed=false;if(held){clearTimeout(held.timer);held.response.destroy();held=null;}for(const request of requests)request.destroy();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}};
}
