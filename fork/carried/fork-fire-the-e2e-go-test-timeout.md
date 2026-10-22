---
id: fork-fire-the-e2e-go-test-timeout
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'ci(fork): fire the e2e go test timeout before the job''s'
upstream:
  outcome: fork-only
  refs: []
  exit: keep
depends_on: []
verified: 2026-10-19
---
# ci(fork): fire the e2e go test timeout before the job's

## Change

`ci(fork): fire the e2e go test timeout before the job's`

## Reason

the hosted runners spend up to 16 minutes on the setup before the e2e tests start, so `go test -timeout=30m` could fire only after the job's 40-minute `timeout-minutes` had cancelled it, and a hung test left no goroutine dump (giantswarm/kagent-upstream#388). `go test` stops at 30 minutes (a green run takes up to 20: a sequential phase of controller-rollout tests of about 11 minutes, then the parallel tests' slot-time over `-parallel`) and the job at 55; the e2e runs six scenarios at once (`E2E_PARALLEL`, upstream four on its eight-vCPU runners), 15 % faster than four on the same revision without memory pressure (giantswarm/kagent-upstream#825)

## History

fork-only: upstream's Blacksmith runners finish the setup in time for its 30/40 pair
