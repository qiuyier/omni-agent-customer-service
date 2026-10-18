export function labOptions(args){
 const allowed=new Set(['--allow-local-postgres','--allow-real-ai','--allow-human-desk','--mock-slow-ai']);
 if(args[0]!=='--allow-local-postgres'||new Set(args).size!==args.length||args.some(arg=>!allowed.has(arg)))throw Error('Requires --allow-local-postgres [--allow-real-ai | --mock-slow-ai] [--allow-human-desk]');
 if(args.includes('--mock-slow-ai')&&args.includes('--allow-real-ai'))throw Error('Mock and real AI are mutually exclusive');
 return {ai:args.includes('--allow-real-ai')||args.includes('--mock-slow-ai'),human:args.includes('--allow-human-desk'),maxExternalCalls:args.includes('--mock-slow-ai')?0:10,...(args.includes('--mock-slow-ai')?{mockSlow:true}:{})};
}
