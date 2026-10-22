---
id: substrate-run-the-line-on-1-17-5
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(substrate): run the charts and the e2e cluster on 1.17.4'
  - 'test(e2e): match the egress gateway''s refusal of agentgateway 2.5 and later'
  - 'fix(substrate): run the charts and the e2e cluster on 1.17.5'
  - 'docs(fork): record the T3, T9, T13, T16 and T18 decisions in the ledger headers (#849)'
upstream:
  outcome: fork-only
  refs: []
  exit: 'fork-only while any of FencingToken, ReportActorExit, EgressPolicyTemplate or ExistingVolume is fork-only in the Substrate line; exits with the last of their themes'
  class: 6
  destination: 'go/go.mod:498 replace and Makefile:92 SUBSTRATE_VERSION on the line'
  first_step: none
  source: n/a
  test: 'make substrate-pin-check'
  recheck: 'each re-pin; the next one moves the module path to go.ate.dev/substrate'
  owner: unassigned
  draft: ""
  decision: issuecomment-6301335159
  couplings: [compat-migrations, credential-injection, durable-pause, new-rpcs, egress-policies, workspace-volumes]
depends_on: [giantswarm/substrate:ateom-keeps-gvisor-sandbox-log, giantswarm/substrate:ateom-reports-workload-container-exit, giantswarm/substrate:ateom-reports-sandbox-exit-of-running-actor, giantswarm/substrate:pause-suspend-resume-take-optional-fencingtoken, giantswarm/substrate:default-egress-policy-actortemplate, giantswarm/substrate:existing-volume-mounted-per-actor-at-sub]
verified: 2026-10-22
---
# fix(substrate): run the charts and the e2e cluster on 1.17.5

## Change

`fix(substrate): run the charts and the e2e cluster on 1.17.5`: the Makefile's `SUBSTRATE_VERSION` names the Substrate line's `1.17.5`, which stamps the published chart's worker image and chart dependency and is what the e2e cluster runs; `fix(substrate): build against the line's 1.17.5`: the `go/go.mod` replace target follows it, once the line builds with Go 1.27.3

## Reason

the Substrate line's `1.17.4` passes a gVisor sandbox's own log to the worker pod's log, each line labelled `ate.actor.container.name: _pause`. Before that release, ateom sent the sandbox's stdio to `/dev/null`. A claude actor whose process gVisor killed about two seconds after `runsc restore` failed its task with no cause anywhere (giantswarm/kagent-upstream#791). With `1.17.5` the sandbox sentry's own log shows the cause in the e2e's `workers.log`: `Systrap context has no stub thread to interrupt`, then `Unexpected SwitchToApp error: systrap corrupted memory: subprocess died` and a SIGKILL while the sandbox keeps running, the systrap stub race of the gVisor nightly of 2026-09-02 (the same kill ended `TestSessionWorkspaceSurvivesAQuestion` with `crashed with no snapshot to return to`, giantswarm/kagent-upstream#808). `1.17.5` runs the gVisor nightly of 2026-09-09, which carries the race fixes; a conversation suspended on the older nightly resumes on it, and the goldens are taken again on the new one. The Makefile's `SUBSTRATE_VERSION` moves alone: the `go/go.mod` replace target stays on `1.12.1`, the same ate-api contract, because Substrate `1.17.5` requires Go 1.27.3. The step changes no persisted format and needs no migration. Substrate 1.16 brought agentgateway 2.5, whose egress refusal names the scheme and the destination (`actor egress policy denied https to <host>:443: it is not in this agent's egress policy …`), so the git-as-the-person e2e matches that text

## History

fork-only: the pin is the line's own

2026-10-22: the current pin of the version-pins theme (decision issuecomment-6301335159), class 6 fork-only and code-bound as substrate-run-the-line-on-1-12-1 was: the `go/go.mod` replace moved to `1.17.5` once the line's go directive reached 1.27.3, so this row carries both halves of the pin and the earlier pin rows squash into it at the next re-pin.

2026-10-22: giantswarm/kagent-upstream#849 landed as one squash commit, `docs(fork): record the T3, T9, T13, T16 and T18 decisions in the ledger headers (#849)`, which carries `fix(substrate): build against the line's 1.17.5` (the `go/go.mod` and `go/go.sum` move to 1.17.5); the row names that commit.
