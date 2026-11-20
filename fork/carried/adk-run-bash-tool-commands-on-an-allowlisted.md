---
id: adk-run-bash-tool-commands-on-an-allowlisted
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(adk): run bash tool commands on an allowlisted environment'
  - 'feat(harness): preserve declared shell environment values'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3002, kagent-dev/kagent#3014]
  exit: 'upstream ships an allowlisted shell environment with explicit literal Harness values; adopt it at the re-pin after subprocess and gateway checks pass'
depends_on: []
verified: 2026-11-20
---
# fix(adk): run bash tool commands on an allowlisted environment

## Change

`fix(adk): run bash tool commands on an allowlisted environment`

## Reason

the Go ADK's bash tool never set `cmd.Env`, so every command a model wrote inherited the runtime's whole environment — the model provider keys the controller injects and every `KAGENT_*` value, `KAGENT_CONFIG_JSON` among them — and its output went back to the model (giantswarm/kagent-upstream#301). The command now runs on an allowlist: `PATH`, `HOME`, `USER`, `LOGNAME`, `LANG`, `LC_*`, `TERM`, `TZ`, `TMPDIR`, the CA-bundle variables a command needs behind the egress gateway, and `PWD` set to the working directory; proxy variables stay out because their URLs may carry credentials

## History

kagent-dev/kagent#3002 remains open. kagent-dev/kagent#3014 adopted an allowlist including the egress CA variables, then was closed unmerged by the stale bot on 2026-11-01; its body still describes the earlier denylist. Current upstream `d5c04c178` preserves the actor environment in `go/adk/pkg/tools/command.go`. No replacement was found in searches for the issue, PR and shell environment work.

2026-11-20: the generic replacement is baseline filtering plus exact non-secret literal values from the selected Harness, serialized separately from runtime credentials. Names do not allow runtime lookups and placeholder formats have no special treatment. A local proposal is prepared against current upstream; publication requires approval. Keep isolation until that mechanism and the platform configuration pass the same subprocess and gateway checks.
