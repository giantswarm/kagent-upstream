#!/usr/bin/env bash
# The release ledger never trails the code (FORK.md, "Releases" and "Which
# kagent are we running"). Two checks, both read FORK.md:
#
#   scripts/fork/ledger-check.sh pin                # every pull request and push
#   scripts/fork/ledger-check.sh releases <version> # the publish pipeline
#
# pin: FORK.md's "Substrate version the pin runs with" row opens with the
# Makefile's SUBSTRATE_VERSION and its "Substrate the pin runs with" row names
# the go.mod replace target's version, so a change of the pin without its two
# rows is red.
#
# releases <version>: every stable release tag below <version> (from
# LEDGER_FROM on) has a row in the Releases table, so a promote whose
# predecessor's row is missing is refused before an image is built. The new
# release's own row is written after its publish: the pipeline's `record` job
# proposes it as a pull request (scripts/fork/releases-row-pr.sh). A row
# counts when it is in the checkout's FORK.md, in the consumed branch's
# (CONSUMED_BRANCH, default giantswarm, fetched from origin), or in the FORK.md
# of that pull request's branch (fork-ledger/releases-<version> on origin): a
# candidate cut before the row's pull request merged is promoted all the same,
# and a row still waiting in its pull request never refuses the next stable.
# A refusal names where the row's pull request is found (REPO_URL). The
# branches are read with one fetch into refs/ledger-check/; a remote read
# that still fails after its retries (READ_ATTEMPTS, READ_DELAY) refuses
# naming git's error, never as a missing row.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
fork=FORK.md
ns=refs/ledger-check
ledger_from=${LEDGER_FROM:-1.6.0}
stable='^v?[0-9]+\.[0-9]+\.[0-9]+$'
problems=()

check_pin() {
  local chart module pins
  pins=$(make -s substrate-pin 2>/dev/null)
  chart=$(sed -n 's/^SUBSTRATE_VERSION=//p' <<<"$pins")
  module=$(sed -n 's/^SUBSTRATE_MODULE_VERSION=//p' <<<"$pins")
  [ -n "$chart" ] && [ -n "$module" ] || { echo "ERROR: cannot read the Substrate pin from the Makefile" >&2; exit 1; }
  grep -qF "| Substrate version the pin runs with | \`${chart}\` " "$fork" ||
    problems+=("the \"Substrate version the pin runs with\" row of ${fork} does not open with the charts' SUBSTRATE_VERSION \`${chart}\`")
  grep -F "| Substrate the pin runs with |" "$fork" | grep -qF "github.com/giantswarm/substrate ${module}" ||
    problems+=("the \"Substrate the pin runs with\" row of ${fork} does not name the go.mod replace target github.com/giantswarm/substrate ${module}")
}

# retry <command>...: runs a remote read up to READ_ATTEMPTS times, READ_DELAY
# seconds apart and growing; the last attempt's error is left in read_error.
read_error=
retry() {
  local attempt
  for ((attempt = 1; attempt <= ${READ_ATTEMPTS:-4}; attempt++)); do
    read_error=$("$@" 2>&1 >/dev/null) && return 0
    [ "$attempt" -lt "${READ_ATTEMPTS:-4}" ] && sleep $((attempt * ${READ_DELAY:-3}))
  done
  read_error=${read_error//$'\n'/ }
  return 1
}

# ledger_of <ref>: FORK.md of a fetched branch into ledger. A blobless clone
# (CircleCI's checkout) fetches the file's blob from origin here, a second
# remote read; one that fails adds a problem and returns 1.
ledger=
ledger_of() {
  if retry git show "$1:$fork"; then
    ledger=$(git show "$1:$fork")
    return 0
  fi
  problems+=("cannot read ${fork} of ${1#"${ns}"/} from origin (git: ${read_error}): its rows were not read; run the gate again")
  return 1
}

check_releases() {
  local release=${1#v} tag version row proposed prs ledgers consumed=${CONSUMED_BRANCH:-giantswarm}
  local repo_url=${REPO_URL:-https://github.com/giantswarm/kagent-upstream}
  [[ "$release" =~ $stable ]] || { echo "${1:-dev build}: not a stable release, the Releases table is not checked"; return; }
  ledgers=$(cat "$fork")
  # One fetch reads the consumed branch and every row pull request's branch.
  # A read that fails is retried and then refuses as a read, never as a
  # missing row.
  if ! retry git fetch -q --prune origin "+refs/heads/${consumed}:${ns}/consumed" \
    "+refs/heads/fork-ledger/releases-*:${ns}/proposed/*"; then
    problems+=("cannot fetch ${consumed} and the row pull requests' branches from origin (git: ${read_error}): the rows were not read; run the gate again")
    return
  fi
  ledger_of "${ns}/consumed" || return
  ledgers+=$'\n'$ledger
  while read -r tag; do
    version=${tag#v}
    [[ "$version" =~ $stable ]] || continue
    [ "$(printf '%s\n%s\n' "$ledger_from" "$version" | sort -V | head -1)" = "$ledger_from" ] || continue
    [ "$(printf '%s\n%s\n' "$version" "$release" | sort -V | head -1)" = "$version" ] && [ "$version" != "$release" ] || continue
    row="| \`${version}\` | \`v${version}\` "
    grep -qF "$row" <<<"$ledgers" && continue
    proposed=fork-ledger/releases-${version}
    prs="${repo_url}/pulls?q=is%3Apr+head%3A${proposed//\//%2F}"
    if git rev-parse -q --verify "${ns}/proposed/${version}" >/dev/null; then
      ledger_of "${ns}/proposed/${version}" || continue
      if grep -qF "$row" <<<"$ledger"; then
        echo "the row of ${version} is not on ${consumed} yet: it waits in the pull request of ${proposed} (${prs}), which counts; merge it"
        continue
      fi
    fi
    problems+=("the Releases table of ${fork} has no row for ${version}: merge the pull request \"docs(fork): the Releases table carries ${version}\" the publish of v${version} opened (${prs}), or run scripts/fork/releases-row.sh ${version}")
  done < <(git tag -l 'v*')
}

case ${1:-} in
  pin) check_pin ;;
  releases) check_releases "${2:-}" ;;
  *) echo "usage: $0 pin | releases <version>" >&2; exit 64 ;;
esac

if [ ${#problems[@]} -gt 0 ]; then
  printf 'ERROR: %s\n' "${problems[@]}" >&2
  echo "Write the rows in the same pull request that changes the pin, or before promoting the next release." >&2
  exit 1
fi
echo "ledger ok (${1})"
