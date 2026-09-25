#!/usr/bin/env bash
# Export the migrations of every release a database of this line may have been
# created by, one directory per distinct migration set, for
# TestUpgradesEveryReleasedSchema (FORK.md, "CI and security"):
#
#   scripts/fork/released-migrations.sh <target-branch> <out-dir>
#
# A release is a stable tag vX.Y.Z of the line (major 1 and above; upstream's
# mirrored tags are 0.x). A pull request to release-X.Y is checked against the
# releases up to X.Y.*, one to the consumed branch against all of them. Tags
# whose migration trees are identical are exported once, under the oldest.
set -euo pipefail
target=${1:?target branch}
out=${2:?output directory}
dir=go/core/pkg/migrations

ceiling=
if [[ $target =~ ^release-([0-9]+)\.([0-9]+)$ ]]; then
  ceiling="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
fi

mkdir -p "$out"
declare -A seen=()
count=0
while read -r tag; do
  if [ -n "$ceiling" ]; then
    minor=${tag#v}
    minor=${minor%.*}
    [ "$(printf '%s\n%s\n' "$minor" "$ceiling" | sort -V | tail -1)" = "$ceiling" ] || continue
  fi
  tree=$(git rev-parse -q --verify "${tag}^{commit}:${dir}" 2>/dev/null) || continue
  if [ -n "${seen[$tree]:-}" ]; then
    echo "${tag}: same migrations as ${seen[$tree]}"
    continue
  fi
  seen[$tree]=$tag
  mkdir -p "${out}/${tag}"
  git archive "$tag" "$dir" | tar -x -C "${out}/${tag}" --strip-components=4
  echo "${tag}: exported"
  count=$((count + 1))
done < <(git tag -l 'v[1-9]*' --sort=v:refname | grep -E '^v[1-9][0-9]*\.[0-9]+\.[0-9]+$')

if [ "$count" -eq 0 ]; then
  echo "::error::no release of the line found for ${target}; fetch the tags (actions/checkout fetch-depth: 0)"
  exit 1
fi
