#!/usr/bin/env bash
# Tests of scripts/fork/sync-stopped.sh, the sync's report of a stopped re-pin,
# against a stub gh that records its calls.
#
#   scripts/fork/sync-stopped_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

mkdir "$work/bin"
# The stub answers `gh issue list` with $STUB_OPEN (a JSON list, after --jq's
# selection is applied by jq) and records every call with its --body-file.
cat > "$work/bin/gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$STUB_LOG"
jq_expr='' body=''
while [ $# -gt 0 ]; do
  case "$1" in
  --jq) jq_expr=$2; shift ;;
  --body-file) body=$2; shift ;;
  esac
  shift
done
[ -n "$body" ] && { echo '--- body'; cat "$body"; echo '--- end'; } >> "$STUB_LOG"
[ -n "$jq_expr" ] && printf '%s' "${STUB_OPEN:-[]}" | jq -r "$jq_expr"
exit 0
EOF
chmod +x "$work/bin/gh"

# run <name> <mode> [args…]: sync-stopped.sh in a fresh directory with the
# fixture report files of $work/<name>.*; the stub's log lands in $log.
run() {
  local name=$1
  shift
  log="$work/$name.log"
  : > "$log"
  (
    cd "$work"
    PATH="$work/bin:$PATH" STUB_LOG="$log" RUN_URL=https://example.test/run/1 \
      GITHUB_REPOSITORY=giantswarm/kagent-upstream \
      REPORT="$work/$name.md" REPORT_ENV="$work/$name.env" \
      "$here/sync-stopped.sh" "$@"
  )
}

expect() {
  local name=$1 pattern=$2
  if grep -qF -- "$pattern" "$log"; then
    echo "ok: $name"
  else
    echo "FAIL: $name: no \"$pattern\" in:" >&2
    sed 's/^/    /' "$log" >&2
    failures=$((failures + 1))
  fi
}

refute() {
  local name=$1 pattern=$2
  if grep -qF -- "$pattern" "$log"; then
    echo "FAIL: $name: \"$pattern\" in:" >&2
    sed 's/^/    /' "$log" >&2
    failures=$((failures + 1))
  else
    echo "ok: $name"
  fi
}

open='[{"number":7,"title":"sync: the weekly re-pin is stopped"},{"number":3,"title":"sync: something else"}]'

# A conflict, no issue open: the issue is created, naming commit and files.
cat > "$work/conflict.env" <<'EOF'
status=conflict
target=12aa4d460aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
candidate=sync/upstream-20261102-12aa4d4
conflict=0f29d8f00bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
conflict_subject=fix(build): the version -X targets follow the Go module path
conflict_files=Makefile,go/core/pkg/consts/labels.go
EOF
{
  printf '### Re-pin `giantswarm` onto `upstream/main`\n\n'
  printf '#### Carried commits (2, in order)\n\n- `aaaaaaa` one carried commit\n- `bbbbbbb` another\n\n'
  printf '#### Conflict\n\nCarried commit `0f29d8f` does not apply.\n'
} > "$work/conflict.md"
STUB_OPEN='[]' run conflict stopped
expect "conflict: creates the issue" 'issue create --repo giantswarm/kagent-upstream --title sync: the weekly re-pin is stopped'
expect "conflict: names the carried commit" '`0f29d8f` fix(build): the version -X targets follow the Go module path'
expect "conflict: names the files" 'in `Makefile`, `go/core/pkg/consts/labels.go`.'
expect "conflict: names the upstream target" 'conflicts with upstream `12aa4d4`'
expect "conflict: keeps the conflict section" '#### Conflict'
expect "conflict: names the run" 'https://example.test/run/1'
refute "conflict: leaves out the carried list" 'one carried commit'

# The same conflict with the issue open: a comment on it, no second issue.
cp "$work/conflict.env" "$work/again.env"
cp "$work/conflict.md" "$work/again.md"
STUB_OPEN=$open run again stopped
expect "open issue: comments on it" 'issue comment 7 --repo giantswarm/kagent-upstream'
refute "open issue: no second issue" 'issue create'

# Lost hunks.
printf 'status=lost-hunks\ntarget=4de6943550000000000000000000000000000000\n' > "$work/hunks.env"
printf '#### Replayed (1)\n\n- `ccccccc` replayed\n\n#### Lost hunks\n\nfoo.go hunk 3 of `ddddddd`\n' > "$work/hunks.md"
STUB_OPEN='[]' run hunks stopped
expect "lost hunks: says so" 'lost carried hunks'
expect "lost hunks: keeps the hunk report" 'foo.go hunk 3 of `ddddddd`'
refute "lost hunks: leaves out the replayed commits" '`ccccccc`'

# Red checks on a rebased candidate.
printf 'status=rebased\ntarget=4de6943550000000000000000000000000000000\ncandidate=sync/upstream-20261109-4de6943\n' > "$work/red.env"
: > "$work/red.md"
STUB_OPEN='[]' CHECKS_RESULT=red CHECKS_STATE='{"ci-ok":"completed/failure"}' run red stopped
expect "red checks: names the candidate and the state" 'The candidate `sync/upstream-20261109-4de6943` on upstream `4de6943` did not pass the checks (red: `{"ci-ok":"completed/failure"}`)'

# A run that stopped before any report (the mirror refused).
STUB_OPEN='[]' run nothing stopped
expect "no report: says the run failed early" 'failed before the re-pin reported a cause (status `none`)'

# A landed run closes the open issue, and does nothing when none is open.
STUB_OPEN=$open run landed landed '`giantswarm` is `abcdef0` on upstream `4de6943`.'
expect "landed: closes the issue" 'issue close 7 --repo giantswarm/kagent-upstream --comment The sync runs again: `giantswarm` is `abcdef0` on upstream `4de6943`. Run: https://example.test/run/1'
STUB_OPEN='[]' run quiet landed
refute "landed, none open: no close" 'issue close'

# An oversized report stays under GitHub's body limit.
cp "$work/conflict.env" "$work/big.env"
{ printf '#### Dropped (9999)\n\n'; for i in $(seq 1 3000); do printf -- '- `%07d` a dropped commit with a long enough subject line\n' "$i"; done; } > "$work/big.md"
STUB_OPEN='[]' run big stopped
expect "big report: still creates the issue" 'issue create --repo giantswarm/kagent-upstream'
size=$(sed -n '/^--- body$/,/^--- end$/p' "$log" | sed '1d;$d' | wc -c)
if [ "$size" -le 60001 ]; then echo "ok: big report: body of $size bytes"; else echo "FAIL: big report: body of $size bytes" >&2; failures=$((failures + 1)); fi

if ((failures)); then
  echo "$failures failure(s)" >&2
  exit 1
fi
