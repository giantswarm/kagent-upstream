#!/usr/bin/env bash
# Whether a change touches documentation only (FORK.md, "CI and security"):
# reads the changed paths on stdin, one per line, and prints `true` when every
# one is documentation no build or test reads, `false` otherwise. ci.yaml and
# image-scan.yaml skip their suites on a docs-only pull request; `fork-ledger`
# still checks FORK.md and fork/carried/.
#
#   gh api --paginate repos/<owner>/<repo>/pulls/<n>/files --jq '.[].filename' | scripts/fork/docs-only.sh
#
# A generated document is not documentation here: its generator's check runs
# in ci.yaml (docs/env.md by `make env-docs-check`,
# docs/architecture/telemetry-contract.md by `make semconv-verify`). No input
# counts as a change of code.
set -euo pipefail

docs='^([^/]+\.md|fork/carried/[^/]+\.md|design/.+|docs/.+)$'
generated='^docs/(env\.md|architecture/telemetry-contract\.md)$'

seen=0
while IFS= read -r path; do
  [[ -z "$path" ]] && continue
  seen=1
  if [[ ! "$path" =~ $docs || "$path" =~ $generated ]]; then
    echo false
    exit 0
  fi
done
if ((seen)); then echo true; else echo false; fi
