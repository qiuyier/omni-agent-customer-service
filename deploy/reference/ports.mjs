import net from 'node:net';
export async function assertAvailable(port){
 const server=net.createServer();
 await new Promise((resolve,reject)=>{server.once('error',()=>reject(Error('reference port occupied')));server.listen(port,'127.0.0.1',resolve);});
 await new Promise(resolve=>server.close(resolve));
}
