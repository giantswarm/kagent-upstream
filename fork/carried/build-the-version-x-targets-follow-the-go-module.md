---
id: build-the-version-x-targets-follow-the-go-module
fork: kagent
theme: ci-publish-infra
category: bugfix
status: carried
commits:
  - 'fix(build): the version -X targets follow the Go module path'
upstream:
  outcome: to-file
  refs: []
  exit: 'the kagent-dev/kagent pull request merged, then the re-pin drops the commit'
  class: 5
  destination: 'kagent-dev/kagent Makefile:104-107 (at 96c40300)'
  first_step: 'open a pull request on kagent-dev/kagent from the source branch: the root Makefile builds the -X targets from github.com/kagent-dev/kagent/go, as go/Makefile:6-9 already does'
  source: 'upstream/version-ldflags-module-path@55e699e93 (on kagent-dev/kagent 96c40300, +4/-4)'
  test: 'with DOCKER_REPO=example/kagent, build the CLI with the root Makefile''s LDFLAGS: kagent version prints the version, commit and build date (upstream main prints dev, none and unknown)'
  recheck: '2026-11-17, then each re-pin'
  owner: unassigned
  draft: issuecomment-6301359498
  decision: issuecomment-6301358702
depends_on: []
verified: 2026-10-20
---
# fix(build): the version -X targets follow the Go module path

## Change

`fix(build): the version -X targets follow the Go module path`

## Reason

the Makefile's `LDFLAGS` built the `-X` targets from `$(DOCKER_REPO)`, the image repository; the line publishes under `giantswarm/kagent`, so the targets missed `github.com/kagent-dev/kagent/go/core/internal/version` and the binaries carried no version, commit or Substrate version. The targets follow the module path from `go/go.mod`. The commit is the build half of the line's first publish commit, whose GitHub Actions publish to `ghcr.io` was superseded by `ci(fork): publish to gsoci from CircleCI` and dropped at the 2026-10-12 re-pin

## History

to file upstream (any build whose image repository differs from the module path)

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): upstream PR on kagent-dev/kagent; the upstream shape hard-codes the module path as `go/Makefile` does, where the line's commit reads it from `go.mod`.
