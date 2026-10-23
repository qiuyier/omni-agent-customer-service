import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
const workflow=readFileSync(new URL('../.github/workflows/recovery.yml',import.meta.url),'utf8');
// Contract checks complement actionlint; they are not a YAML parser or a cloud run.
test('CI uses read-only credentials and no privileged PR trigger',()=>{
 assert.match(workflow,/permissions:\s+contents: read/);
 assert.match(workflow,/persist-credentials: false/);
 assert.doesNotMatch(workflow,/pull_request_target|secrets\.|continue-on-error/);
 assert.match(workflow,/  push:/);assert.match(workflow,/  pull_request:/);
});
test('database mode is explicit dispatch only and uses local Docker',()=>{
 assert.match(workflow,/type: boolean/);assert.match(workflow,/default: false/);
 assert.match(workflow,/github.event_name == 'workflow_dispatch' && inputs.postgres/);
 assert.match(workflow,/host=unix:\/\/\/var\/run\/docker.sock/);
 assert.match(workflow,/--postgres --allow-test-writes/);
 assert.match(workflow,/else\s+node scripts\/recovery_check.mjs 2>&1/);
});
test('failures propagate through tee and artifact collection always runs',()=>{
 assert.match(workflow,/shell: bash/);assert.match(workflow,/set -o pipefail/);
 assert.match(workflow,/if: always\(\)\s+uses: actions\/upload-artifact@v4/);
 assert.match(workflow,/omni-recovery-check-\*\/report.json/);
 assert.match(workflow,/retention-days: 14/);
 assert.doesNotMatch(workflow,/path:.*github.workspace|include-hidden-files: true/);
});
