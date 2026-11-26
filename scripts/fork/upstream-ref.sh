#!/usr/bin/env bash
# The upstream ref the line is rebased onto (FORK.md, "Re-pin"): the one line
# of fork/upstream-ref, a full ref of kagent-dev/kagent — `refs/heads/main`, a
# release branch `refs/heads/release/vX.Y.x`, or a release tag
# `refs/tags/vX.Y.Z`. The pin is the merge base of the line with it.
#
#   scripts/fork/upstream-ref.sh          # the full upstream ref
#   scripts/fork/upstream-ref.sh name     # its short name: main, release/v1.0.x, v1.0.1
#   scripts/fork/upstream-ref.sh mirror   # its mirror in a clone of this repository:
#                                         # origin/<branch>, or refs/tags/upstream/<tag>
#
# UPSTREAM_REF_FILE names another file. Upstream's tags are mirrored under
# refs/tags/upstream/ (scripts/fork/mirror.sh): the bare `v*` names are the
# line's own releases.
set -euo pipefail

file=${UPSTREAM_REF_FILE:-$(cd "$(dirname "$0")/../.." && pwd)/fork/upstream-ref}
ref=$(sed -n '/^[[:space:]]*#/d; /[^[:space:]]/{s/[[:space:]]//g; p; q;}' "$file")
case $ref in
  refs/heads/?* | refs/tags/?*) ;;
  *)
    echo "${file}: '${ref}' is not a full upstream ref (refs/heads/<branch> or refs/tags/<tag>)" >&2
    exit 1
    ;;
esac

case ${1:-ref} in
  ref) echo "$ref" ;;
  name)
    name=${ref#refs/heads/}
    echo "${name#refs/tags/}"
    ;;
  mirror)
    case $ref in
      refs/heads/*) echo "origin/${ref#refs/heads/}" ;;
      refs/tags/*) echo "refs/tags/upstream/${ref#refs/tags/}" ;;
    esac
    ;;
  *)
    echo "usage: $0 [ref|name|mirror]" >&2
    exit 64
    ;;
esac
