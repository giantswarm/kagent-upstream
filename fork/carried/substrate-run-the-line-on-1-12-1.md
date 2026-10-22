---
id: substrate-run-the-line-on-1-12-1
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'chore(substrate): run the line on the stable 1.12.1'
upstream:
  outcome: fork-only
  refs: []
  exit: 'squashed into the current pin at the next re-pin; this file is deleted then'
  class: 1
  destination: ""
  first_step: 'none; squashed into the current pin at the next re-pin'
  source: n/a
  test: 'scripts/fork/carried.sh --check after the squash'
  recheck: 'at the re-pin'
  owner: unassigned
  draft: ""
  decision: issuecomment-6301335159
depends_on: [giantswarm/substrate:ateom-reports-workload-container-exit, giantswarm/substrate:ateom-reports-sandbox-exit-of-running-actor, giantswarm/substrate:pause-suspend-resume-take-optional-fencingtoken, giantswarm/substrate:default-egress-policy-actortemplate, giantswarm/substrate:existing-volume-mounted-per-actor-at-sub]
verified: 2026-10-22
---
# chore(substrate): run the line on the stable 1.12.1

## Change

`chore(substrate): run the line on the stable 1.12.1`

## Reason

the Substrate line's `1.12.1` reports an application container that exits while its gVisor sandbox keeps running (giantswarm/substrate#464): on `1.11.1` the pause container kept the sandbox up, the Actor stayed `RUNNING` with nothing behind it, the turn's task stayed `working` with no settled boundary, so `DeleteSession` was refused as unsettled and a scheduled execution never ended (giantswarm/kagent-upstream#649). The `go/go.mod` replace target and the Makefile's `SUBSTRATE_VERSION` move to it together; the step changes no persisted format, and the ate-api proto only adds RPCs

## History

fork-only: the pin is the line's own

2026-10-20: decided in the version-pins theme review (decision issuecomment-6301335159): class 6 fork-only, code-bound: the controller compiles against `FencingToken` (core/internal/service/session/quiescence.go), `SessionExistingVolumes` (workflow.go), `EgressPolicyTemplate` (core/internal/substrate/actor_template.go) and `ReportActorExit`, none of which the ateapi proto of kagent-dev/substrate or agent-substrate/substrate `main` has; the pin exits with the last of the four Substrate-line rows it depends on (durable-pause, new-rpcs, egress-policies, workspace-volumes). Re-pin cost: upstream `main` at `2a50a8be` replaces `go.ate.dev/substrate => github.com/kagent-dev/substrate v0.6.0-alpha3`, so the replace and 84 import sites change module path at the next re-pin, on the same re-pin as the compat-migrations conversion rows and the credential-injection compiler rows (both themes undecided on 2026-10-20).

2026-10-22: class 1 drop: superseded by substrate-run-the-line-on-1-17-5 once the `go/go.mod` replace moved to `1.17.5`; squashed into the current pin at the next re-pin, this file deleted then (carried.sh needs it until). The code-bound reasons above, and the module-path cost of the next re-pin, are carried by that row.
