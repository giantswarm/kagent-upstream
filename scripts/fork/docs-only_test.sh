#!/usr/bin/env bash
# Tests of scripts/fork/docs-only.sh, the docs-only classifier of ci.yaml and
# image-scan.yaml.
#
#   scripts/fork/docs-only_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
failures=0

expect() {
  local want=$1 name=$2 got
  shift 2
  got=$(printf '%s\n' "$@" | "$here/docs-only.sh")
  if [[ "$got" == "$want" ]]; then
    echo "ok: $name"
  else
    echo "FAIL: $name: got $got, want $want" >&2
    failures=$((failures + 1))
  fi
}

expect true "the ledger" FORK.md
expect true "a carried-change file" fork/carried/fork-test-and-scan-the-consumed-branch-re.md
expect true "root documents, design and docs" README.md design/template.md docs/architecture/overview.md
expect false "a workflow" FORK.md .github/workflows/ci.yaml
expect false "Go code" go/core/cmd/controller/main.go
expect false "a nested README" python/packages/kagent-core/README.md
expect false "an embedded template" go/core/cli/internal/mcp/frameworks/golang/templates/README.md
expect false "the generated environment reference" docs/env.md
expect false "the generated telemetry contract" docs/architecture/telemetry-contract.md
expect false "the ledger's exemptions" fork/EXEMPT
expect false "no change at all"

if ((failures)); then
  echo "$failures failure(s)" >&2
  exit 1
fi
