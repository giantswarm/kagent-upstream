#!/usr/bin/env bash
# The sync's report of a stopped re-pin (FORK.md, "Re-pin"): one open issue,
# titled $TITLE, stands for as long as the sync is stopped.
#
#   scripts/fork/sync-stopped.sh stopped   # a run stopped: open the issue, or comment on it
#   scripts/fork/sync-stopped.sh landed    # a run landed or found nothing to do: close it
#
# The cause comes from the re-pin's report (repin.sh): $REPORT_ENV (status,
# target, conflict, conflict_subject, conflict_files, candidate) and $REPORT,
# whose long commit lists are left out; and from the checks' wait:
# $CHECKS_RESULT (red, timeout) and $CHECKS_STATE. A run that stopped before a
# report (the mirror refused, a script failed) is reported as such. $RUN_URL
# names the run; gh writes to $GITHUB_REPOSITORY with $GH_TOKEN.
set -euo pipefail

TITLE='sync: the weekly re-pin is stopped'
REPORT=${REPORT:-repin-report.md}
REPORT_ENV=${REPORT_ENV:-repin-report.env}
CHECKS_RESULT=${CHECKS_RESULT:-}
CHECKS_STATE=${CHECKS_STATE:-}
RUN_URL=${RUN_URL:?}
REPO=${GITHUB_REPOSITORY:?}
# GitHub refuses an issue body or comment above 65536 characters.
MAX_BODY=60000

# report_value <key>: the last value repin.sh wrote for <key>, or nothing.
report_value() {
  [ -f "$REPORT_ENV" ] || return 0
  sed -n "s/^$1=//p" "$REPORT_ENV" | tail -n 1
}

# report_sections: the report without its per-commit lists (the carried and the
# replayed commits, hundreds of lines); the pin table, the conflict, the drops
# and the lost hunks stay.
report_sections() {
  [ -f "$REPORT" ] || return 0
  awk '/^#### (Carried commits|Replayed) /{skip=1; next} /^#+ /{skip=0} !skip' "$REPORT"
}

# cause: one markdown paragraph naming why the run stopped.
cause() {
  local status target conflict subject files
  status=$(report_value status)
  target=$(report_value target)
  case "$status" in
  conflict)
    conflict=$(report_value conflict)
    subject=$(report_value conflict_subject)
    files=$(report_value conflict_files)
    printf 'A carried commit conflicts with upstream `%s`: `%s` %s, in %s.\n' \
      "${target:0:7}" "${conflict:0:7}" "$subject" "$(code_list "$files")"
    ;;
  lost-hunks)
    printf 'The rebase onto upstream `%s` lost carried hunks; the report below names each commit, file and hunk.\n' "${target:0:7}"
    ;;
  *)
    if [ -n "$CHECKS_RESULT" ]; then
      printf 'The candidate `%s` on upstream `%s` did not pass the checks (%s: `%s`); the branch stays for the fix.\n' \
        "$(report_value candidate)" "${target:0:7}" "$CHECKS_RESULT" "$CHECKS_STATE"
    else
      printf 'The run failed before the re-pin reported a cause (status `%s`): the mirror refused a ref or a step failed; its log names it.\n' \
        "${status:-none}"
    fi
    ;;
  esac
}

# code_list <a,b,c>: `a`, `b`, `c`.
code_list() {
  local IFS=, out='' f
  for f in $1; do out+="${out:+, }\`$f\`"; done
  printf '%s' "${out:-no file reported}"
}

stopped_body() {
  printf '### Run %s stopped\n\n' "$(date -u +'%Y-%m-%d %H:%M UTC')"
  cause
  printf '\nNothing was pushed to the consumed branch. Finish by hand (FORK.md, "Manual re-pin"); the next landed re-pin closes this issue. Run: %s\n\n' "$RUN_URL"
  report_sections
}

# capped <file>: <file> cut to $MAX_BODY bytes in place. Cut from a file, never
# with head in a pipe, whose early close would kill the writer with SIGPIPE.
capped() {
  head -c "$MAX_BODY" "$1" > "$1.cut"
  mv "$1.cut" "$1"
}

# open_issue: the number of the open issue titled $TITLE, or nothing.
open_issue() {
  gh issue list --repo "$REPO" --state open --limit 100 --json number,title \
    --jq "map(select(.title == \"$TITLE\")) | first | .number // empty"
}

body=$(mktemp)
trap 'rm -f "$body" "$body.cut"' EXIT

case "${1:-}" in
stopped)
  n=$(open_issue)
  if [ -n "$n" ]; then
    stopped_body > "$body"
    capped "$body"
    gh issue comment "$n" --repo "$REPO" --body-file "$body"
  else
    {
      printf 'The weekly re-pin of the consumed branch (`.github/workflows/sync-upstream.yaml`) is stopped. Every stopped run adds a comment; the next landed re-pin closes this issue.\n\n'
      stopped_body
    } > "$body"
    capped "$body"
    gh issue create --repo "$REPO" --title "$TITLE" --body-file "$body"
  fi
  ;;
landed)
  n=$(open_issue)
  [ -n "$n" ] || exit 0
  printf 'The sync runs again: %s Run: %s\n' "${2:-the re-pin landed.}" "$RUN_URL" > "$body"
  gh issue close "$n" --repo "$REPO" --comment "$(cat "$body")"
  ;;
*)
  echo "usage: $0 stopped|landed [<landed message>]" >&2
  exit 64
  ;;
esac
