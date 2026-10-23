---
id: build-retry-go-mod-download-on-a-transient-proxy
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'fix(build): retry go mod download on a transient proxy error'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3167]
  exit: 'kagent-dev/kagent#3167 merged with these loops, then the re-pin drops the commit; expected instead: fork-only fork infrastructure from 2026-11-17 with #3167 kept as the exit (acceptance unlikely: no upstream-reported symptom)'
  class: 5
  destination: 'kagent-dev/kagent#3167, extended with the go mod download loops of go/Dockerfile, go/harness/claude/Dockerfile and go/harness/codex/Dockerfile'
  first_step: 'mark kagent-dev/kagent#3167 ready for review once its author has read the rebased diff'
  source: 'upstream/apk-retry@8aae65968, the second commit of kagent-dev/kagent#3167 (this files commit without the ledger file, rebased onto main on 2026-10-23)'
  test: 'build go/Dockerfile with GOPROXY set to a proxy that fails the first two requests: the build passes on the third attempt, and a missing module still fails after three'
  recheck: '2026-11-17, then each re-pin'
  owner: teemow
  draft: issuecomment-6301360144
  decision: issuecomment-6301358702
depends_on: [build-retry-the-alpine-package-step-on-a]
verified: 2026-10-23
---
# fix(build): retry go mod download on a transient proxy error

## Change

`fix(build): retry go mod download on a transient proxy error`

## Reason

the `build (claude-harness)` job failed on one dropped stream from proxy.golang.org (`stream error: stream ID 1; INTERNAL_ERROR; received from peer`, then `go mod download` did not complete) in a pull request that did not touch the image (giantswarm/kagent-upstream#448, giantswarm/kagent-upstream#449). The module download step of `go/Dockerfile` (the controller and the other Go binaries), `go/harness/claude` and `go/harness/codex` runs in the same loop as the Alpine step: three attempts, 5 s apart; a missing module still fails after the third with Go's error and exit code. The image content is unchanged

## History

none — CI of the fork line (the image builds of this repository's pipeline), not upstream code; no upstream branch

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): upstream PR, folded into kagent-dev/kagent#3167 with the Alpine retry; upstream's `go mod download` (`go/Dockerfile:17-19` at `96c40300`) runs under a BuildKit cache mount, so the failure is a cold-cache one.

2026-10-23: folded into kagent-dev/kagent#3167 as its second commit.
