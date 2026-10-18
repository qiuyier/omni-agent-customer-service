import test from 'node:test';
import assert from 'node:assert/strict';
import {labOptions} from './lab-options.mjs';
test('AI and human desk are independently explicit; external budget is ten',()=>{
 for(const flags of [[],['--allow-real-ai'],['--allow-human-desk'],['--allow-real-ai','--allow-human-desk']])assert.deepEqual(labOptions(['--allow-local-postgres',...flags]),{ai:flags.includes('--allow-real-ai'),human:flags.includes('--allow-human-desk'),maxExternalCalls:10});
});
test('lab rejects implicit writes, duplicate or unknown flags',()=>{
 for(const flags of [[],['--allow-real-ai'],['--allow-local-postgres','--allow-real-ai','--allow-real-ai'],['--allow-local-postgres','--max-calls=100'],['--allow-local-postgres','extra']])assert.throws(()=>labOptions(flags));
});
