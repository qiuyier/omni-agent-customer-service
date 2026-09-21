#!/usr/bin/env bash
set -euo pipefail

# A separate local PostgreSQL volume, never CRMEB or TEST_DATABASE_URL. The
# database is intentionally retained so the persistent demo can be inspected.
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
db_port="${WEBCHAT_POSTGRES_PORT:-15433}"
if [[ ! "$db_port" =~ ^[0-9]{1,5}$ ]] || (( 10#$db_port < 1 || 10#$db_port > 65535 )); then
  printf '%s\n' 'WEBCHAT_POSTGRES_PORT must be 1..65535' >&2
  exit 1
fi
export WEBCHAT_POSTGRES_PORT="$db_port"
docker compose -f deploy/webchat/compose.yaml up -d --wait postgres

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/webchat-restart.XXXXXX")"
# macOS TMPDIR commonly ends in '/'; pass canonical paths to the checker.
work_dir="$(cd "$work_dir" && pwd -P)"
CGO_ENABLED=0 go build -o "$work_dir/migrate" ./cmd/migrate
CGO_ENABLED=0 go build -o "$work_dir/webchat-server" ./cmd/webchat-server
CGO_ENABLED=0 go build -o "$work_dir/webchat-demo" ./cmd/webchat-demo
CGO_ENABLED=0 go build -o "$work_dir/webchat-restart-check" ./cmd/webchat-restart-check
demo_url="postgres://webchat_demo:webchat_demo_local@127.0.0.1:${db_port}/omni_agent_webchat_demo?sslmode=disable"

# Do not inherit PG*, model keys, proxies, or deployment credentials into a
# migration/check/child process. Values here are public local-demo credentials.
env -i PATH="$PATH" DATABASE_URL="$demo_url" "$work_dir/migrate"
env -i PATH="$PATH" WEBCHAT_CHECK_DATABASE_URL="$demo_url" \
  "$work_dir/webchat-restart-check" \
  -server-binary "$work_dir/webchat-server" \
  -demo-binary "$work_dir/webchat-demo" \
  -output-dir "$work_dir/report"
printf '%s\n' 'Local PostgreSQL and its demo volume were retained. No databases were truncated or deleted.'
