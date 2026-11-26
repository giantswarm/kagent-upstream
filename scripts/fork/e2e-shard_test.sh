#!/usr/bin/env bash
# Tests of scripts/fork/e2e-shard.sh, the e2e test split of ci.yaml.
#
#   scripts/fork/e2e-shard_test.sh
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
failures=0

fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

names=(TestAlpha TestBeta TestGamma TestDelta TestEpsilon TestZeta TestEta TestTheta)
list() { printf '%s\n' "${names[@]}" "ok  	github.com/kagent-dev/kagent/go/core/test/e2e	0.01s"; }
tests_of() { sed -E 's/^\^\((.*)\)\$$/\1/' <<<"$1" | tr '|' '\n'; }

shard0=$(list | "$here/e2e-shard.sh" 0 2)
shard1=$(list | "$here/e2e-shard.sh" 1 2)
union=$( { tests_of "$shard0"; tests_of "$shard1"; } | sort)
if [[ "$union" == "$(printf '%s\n' "${names[@]}" | sort)" ]]; then
  echo "ok: the shards together run every test once"
else
  fail "the shards' union is not the test list: $union"
fi

if [[ "$shard0" =~ ^\^\(Test[A-Za-z0-9_|]+\)\$$ ]]; then
  echo "ok: the pattern is anchored"
else
  fail "the pattern is not anchored: $shard0"
fi

grown=$( (list; echo TestIota) | "$here/e2e-shard.sh" 0 2)
if [[ "$(tests_of "$grown" | grep -vx TestIota)" == "$(tests_of "$shard0")" ]]; then
  echo "ok: a new test moves no other test"
else
  fail "a new test moved others: $shard0 -> $grown"
fi

if printf 'TestMain\n' | "$here/e2e-shard.sh" 0 1 >/dev/null 2>&1; then
  fail "a shard with no test passed"
else
  echo "ok: a shard with no test fails"
fi

for args in "2 2" "0 0" "x 2" "0"; do
  # shellcheck disable=SC2086
  if list | "$here/e2e-shard.sh" $args >/dev/null 2>&1; then
    fail "usage '$args' was accepted"
  else
    echo "ok: usage '$args' is refused"
  fi
done

if ((failures)); then
  echo "$failures failure(s)" >&2
  exit 1
fi
