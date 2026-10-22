#!/usr/bin/env bash
# The line's own migrations in upstream's `core` source are frozen at the
# files listed below (FORK.md, "Before you carry a patch"). Upstream edits its
# unreleased baseline in place, and every installation's database has to be
# converted to it at the re-pin past Substrate GA; each file added here would
# grow that conversion. New schema of the line goes into an extra migration
# source (app.Options.ExtraMigrations) instead.
#
#   scripts/fork/core-migrations.sh   # exit 1 if a core migration the pin lacks is not listed
#
# The pin is the merge base with the mirror (MIRROR, default origin/main), as
# in scripts/fork/carried.sh.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
dir=go/core/pkg/migrations/core

frozen=(
	000002_quiescence_claim_lease.sql
	000003_session_deletion_reason_agent_deleted.sql
	000004_quiescence_fencing_generation.sql
	000005_push_notification_tables.sql
	000006_session_owner_groups.sql
	000007_runtime_revision_caller_credential.sql
)

pin=$(git -C "$root" merge-base HEAD "${MIRROR:-origin/main}")
declare -A upstream=()
while IFS= read -r name; do
	upstream[$name]=1
done < <(git -C "$root" ls-tree --name-only "$pin" "$dir/" | xargs -r -n1 basename)

declare -A allowed=()
for name in "${frozen[@]}"; do
	allowed[$name]=1
done

problems=0
shopt -s nullglob
for file in "$root/$dir"/*.sql; do
	name=$(basename "$file")
	if [[ -z ${upstream[$name]:-} && -z ${allowed[$name]:-} ]]; then
		printf '%s/%s: a core migration the pin does not have; put the line'\''s schema into an extra migration source (app.Options.ExtraMigrations)\n' "$dir" "$name" >&2
		problems=$((problems + 1))
	fi
done

((problems == 0))
