export function labOptions(args){
 const allowed=new Set(['--allow-local-postgres','--allow-real-ai','--allow-human-desk']);
 if(args[0]!=='--allow-local-postgres'||new Set(args).size!==args.length||args.some(arg=>!allowed.has(arg)))throw Error('Requires --allow-local-postgres [--allow-real-ai] [--allow-human-desk]');
 return {ai:args.includes('--allow-real-ai'),human:args.includes('--allow-human-desk'),maxExternalCalls:10};
}
