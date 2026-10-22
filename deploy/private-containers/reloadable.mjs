// Serial reload of test transport/support only. The business IAM remains alive.
export function reloadable({readConfig,validate,start}) {
 let active=null,queue=Promise.resolve(),closed=false;
 const reload=()=>{
  const next=queue.then(async()=>{
   if(closed)throw Error('closed');
   const config=await readConfig();await validate(config);
   const previous=active;active=null;await previous?.close();
   active=await start(config);
  });queue=next.catch(()=>{});return next;
 };
 return {reload,async handle(req,res,...args){if(active)return active.handle(req,res,...args);res.writeHead(503,{'Content-Type':'application/json','Cache-Control':'no-store'}).end('{"error":"support_reloading"}');return true;},async close(){closed=true;await queue;const previous=active;active=null;await previous?.close();}};
}
