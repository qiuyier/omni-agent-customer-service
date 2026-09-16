#!/usr/bin/env bash
set -euo pipefail

# Build the real child executable first; never kill a go-run wrapper.
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_dir"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/integration-acceptance.XXXXXX")"
CGO_ENABLED=0 go build -o "$work_dir/integration-check" ./cmd/integration-check
"$work_dir/integration-check" -output-dir "$work_dir/run"
# Fictional journals, executable and report are intentionally retained for inspection.
