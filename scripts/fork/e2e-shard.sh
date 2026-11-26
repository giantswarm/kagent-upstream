#!/usr/bin/env bash
# The e2e tests one leg of ci.yaml's test-e2e matrix runs: reads top-level test
# names on stdin, one per line (`go test -list`), and prints the anchored
# `go test -run` pattern of the tests whose name hashes to shard <index> of
# <count>. A test's shard depends on its name alone, so adding or removing a
# test never moves another. A shard with no test is an error, not a green run.
#
#   go test -list . ./core/test/e2e | scripts/fork/e2e-shard.sh 0 2
set -euo pipefail

if [[ $# -ne 2 || ! "$1" =~ ^[0-9]+$ || ! "$2" =~ ^[1-9][0-9]*$ || "$1" -ge "$2" ]]; then
  echo "usage: $0 <index> <count>, 0 <= index < count" >&2
  exit 2
fi
index=$1 count=$2

selected=()
while IFS= read -r name; do
  [[ "$name" =~ ^Test[A-Za-z0-9_]+$ ]] || continue
  [[ "$name" == TestMain ]] && continue
  hash=$(printf '%s' "$name" | cksum | cut -d' ' -f1)
  if ((hash % count == index)); then
    selected+=("$name")
  fi
done

if ((${#selected[@]} == 0)); then
  echo "shard $index of $count has no test" >&2
  exit 1
fi
(IFS='|'; printf '^(%s)$\n' "${selected[*]}")
