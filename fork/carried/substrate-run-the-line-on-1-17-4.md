---
id: substrate-run-the-line-on-1-17-4
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(substrate): run the charts and the e2e cluster on 1.17.4'
  - 'test(e2e): match the egress gateway''s refusal of agentgateway 2.5 and later'
upstream:
  outcome: fork-only
  refs: []
  exit: keep
depends_on: [giantswarm/substrate:ateom-keeps-gvisor-sandbox-log]
verified: 2026-10-21
---
# fix(substrate): run the charts and the e2e cluster on 1.17.4

## Change

`fix(substrate): run the charts and the e2e cluster on 1.17.4`

## Reason

the Substrate line's `1.17.4` passes a gVisor sandbox's own log to the worker pod's log, each line labelled `ate.actor.container.name: _pause`. Before that release, ateom sent the sandbox's stdio to `/dev/null`. A claude actor whose process gVisor killed about two seconds after `runsc restore` failed its task with no cause anywhere (giantswarm/kagent-upstream#791). With `1.17.4` the cause shows in the e2e's `workers.log`. The Makefile's `SUBSTRATE_VERSION` moves alone: the `go/go.mod` replace target stays on `1.12.1`, the same ate-api contract, because Substrate `1.17.4` requires Go 1.27.3. The step changes no persisted format and needs no migration. Substrate 1.16 brought agentgateway 2.5, whose egress refusal names the scheme and the destination (`actor egress policy denied https to <host>:443: it is not in this agent's egress policy …`), so the git-as-the-person e2e matches that text

## History

fork-only: the pin is the line's own
