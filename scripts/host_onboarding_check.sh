#!/usr/bin/env bash
# Local reference acceptance only. Never consumes the caller's deployment config.
set -euo pipefail
if [[ "$#" != 1 || "$1" != --allow-test-writes ]]; then
  printf '%s\n' 'requires --allow-test-writes (dedicated localhost test PostgreSQL only)' >&2
  exit 1
fi
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
check_dir="$(mktemp -d "${TMPDIR:-/tmp}/omni-onboarding.XXXXXX")"
check_dir="$(cd "$check_dir" && pwd -P)"
CGO_ENABLED=0 go build -o "$check_dir/webchat-server" ./cmd/webchat-server
if env -i PATH="$PATH" "$check_dir/webchat-server" -check-config; then
  printf '%s\n' 'FAIL: empty configuration was accepted' >&2
  exit 1
fi
printf '%s\n' 'PASS: empty configuration rejected'
# This existing script explicitly migrates only the dedicated local test DB and
# runs the actual child-process persistence checks. Retains DB, volume, reports.
env -i PATH="$PATH" HOME="$HOME" WEBCHAT_POSTGRES_PORT=15433 bash scripts/webchat_restart_check.sh
host_a="$(openssl rand -hex 24)"
host_b="$(openssl rand -hex 24)"
agent_a="$(openssl rand -hex 24)"
agent_b="$(openssl rand -hex 24)"
test_dsn='postgres://webchat_demo:webchat_demo_local@127.0.0.1:15433/omni_agent_webchat_demo?sslmode=disable'
for check in -check-config -check-resources; do
  env -i PATH="$PATH" DATABASE_URL="$test_dsn" WEBCHAT_HOST_ID=onboarding-single \
    WEBCHAT_HOST_SECRET="$host_a" WEBCHAT_REPLY_MODE=fake "$check_dir/webchat-server" "$check"
  env -i PATH="$PATH" DATABASE_URL="$test_dsn" WEBCHAT_REPLY_MODE=fake \
    WEBCHAT_HOSTS_JSON="[{\"source_id\":\"onboarding-a\",\"secret\":\"$host_a\"},{\"source_id\":\"onboarding-b\",\"secret\":\"$host_b\"}]" \
    WEBCHAT_AGENTS_JSON="[{\"source_id\":\"onboarding-a\",\"id\":\"staff-1\",\"secret\":\"$agent_a\"},{\"source_id\":\"onboarding-b\",\"id\":\"staff-1\",\"secret\":\"$agent_b\"}]" \
    "$check_dir/webchat-server" "$check"
done
printf '%s\n' 'PASS: clean-environment single/multi-host configuration, resources and persistence checks'
printf '%s\n' 'Database/volume and temporary build/report directories retained. Stop PostgreSQL if this run started it. No target business or production approval implied.'
