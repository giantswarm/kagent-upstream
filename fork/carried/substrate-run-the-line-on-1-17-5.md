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
upstream:
  outcome: fork-only
  refs: []
  exit: keep
depends_on: [giantswarm/substrate:ateom-keeps-gvisor-sandbox-log]
verified: 2026-10-21
---
# fix(substrate): run the charts and the e2e cluster on 1.17.5

## Change

`fix(substrate): run the charts and the e2e cluster on 1.17.5`: the Makefile's `SUBSTRATE_VERSION` names the Substrate line's `1.17.5`, which stamps the published chart's worker image and chart dependency and is what the e2e cluster runs

## Reason

the Substrate line's `1.17.4` passes a gVisor sandbox's own log to the worker pod's log, each line labelled `ate.actor.container.name: _pause`. Before that release, ateom sent the sandbox's stdio to `/dev/null`. A claude actor whose process gVisor killed about two seconds after `runsc restore` failed its task with no cause anywhere (giantswarm/kagent-upstream#791). With `1.17.5` the sandbox sentry's own log shows the cause in the e2e's `workers.log`: `Systrap context has no stub thread to interrupt`, then `Unexpected SwitchToApp error: systrap corrupted memory: subprocess died` and a SIGKILL while the sandbox keeps running, the systrap stub race of the gVisor nightly of 2026-09-02 (the same kill ended `TestSessionWorkspaceSurvivesAQuestion` with `crashed with no snapshot to return to`, giantswarm/kagent-upstream#808). `1.17.5` runs the gVisor nightly of 2026-09-09, which carries the race fixes; a conversation suspended on the older nightly resumes on it, and the goldens are taken again on the new one. The Makefile's `SUBSTRATE_VERSION` moves alone: the `go/go.mod` replace target stays on `1.12.1`, the same ate-api contract, because Substrate `1.17.5` requires Go 1.27.3. The step changes no persisted format and needs no migration. Substrate 1.16 brought agentgateway 2.5, whose egress refusal names the scheme and the destination (`actor egress policy denied https to <host>:443: it is not in this agent's egress policy …`), so the git-as-the-person e2e matches that text

## History

fork-only: the pin is the line's own
