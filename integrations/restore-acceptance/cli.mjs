import {fileURLToPath} from 'node:url';
import {resolve} from 'node:path';
import {acceptRestore} from './core.mjs';
import {createCRMEB, validateConfig} from './crmeb.mjs';

export async function main(args = process.argv.slice(2), input = process.stdin, output = process.stdout) {
  let report;
  try {
    const allowed = ['--run', '--confirm-isolated-copy', '--confirm-public-entry-offline'];
    if (new Set(args).size !== args.length || args.some(a => !allowed.includes(a))) throw Error('invalid_arguments');
    const run = args.includes('--run');
    if (run && !allowed.every(a => args.includes(a))) throw Error('confirmation_required');
    let text = ''; for await (const chunk of input) { text += chunk; if (Buffer.byteLength(text) > 65536) throw Error('input_limit'); }
    const c = validateConfig(JSON.parse(text), run);
    report = run ? await acceptRestore(createCRMEB(c)) : {
      schema: 1, mode: 'plan', executed: false, support_may_open: false,
      checks: ['old_credentials_denied', 'fresh_logins', 'customer_isolation', 'receipt_and_deduplication', 'human_lifecycle', 'stable_history', 'logout'],
    };
  } catch { report = {schema: 1, passed: false, support_may_open: false, error: 'invalid_input_or_configuration'}; }
  output.write(JSON.stringify(report, null, 2) + '\n');
  return report.passed === false ? 1 : 0;
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) process.exitCode = await main();
