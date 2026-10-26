---
id: build-the-version-x-targets-follow-the-go-module
fork: kagent
theme: ci-publish-infra
category: bugfix
status: carried
commits:
  - 'fix(build): the version -X targets follow the Go module path'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3376]
  exit: 'drops at the re-pin onto a commit containing 7e02b577'
  class: 5
  destination: 'kagent-dev/kagent#3376, merged 2026-10-26 as 7e02b577'
  first_step: 'leave the commit out at the first re-pin onto a commit containing 7e02b577'
  source: 'upstream/version-ldflags-module-path-2@d887ca755 (55e699e93 cherry-picked onto kagent-dev/kagent e480daa3f, +4/-4)'
  test: 'after the re-pin, with DOCKER_REPO=example/kagent, build the CLI with the root Makefile''s LDFLAGS: kagent version prints the version, commit and build date'
  recheck: 'the next re-pin'
  owner: fiunchinho
  draft: issuecomment-6301359498
  decision: issuecomment-6301358702
depends_on: []
verified: 2026-10-27
---
# fix(build): the version -X targets follow the Go module path

## Change

`fix(build): the version -X targets follow the Go module path`

## Reason

the Makefile's `LDFLAGS` built the `-X` targets from `$(DOCKER_REPO)`, the image repository; the line publishes under `giantswarm/kagent`, so the targets missed `github.com/kagent-dev/kagent/go/core/internal/version` and the binaries carried no version, commit or Substrate version. The targets follow the module path from `go/go.mod`. The commit is the build half of the line's first publish commit, whose GitHub Actions publish to `ghcr.io` was superseded by `ci(fork): publish to gsoci from CircleCI` and dropped at the 2026-10-12 re-pin

## History

to file upstream (any build whose image repository differs from the module path)

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): upstream PR on kagent-dev/kagent; the upstream shape hard-codes the module path as `go/Makefile` does, where the line's commit reads it from `go.mod`.

2026-10-26: filed as kagent-dev/kagent#3376 by fiunchinho from `upstream/version-ldflags-module-path-2`, the prepared commit cherry-picked onto upstream `main` `e480daa3f` with no conflict. Verified with `DOCKER_REPO=example/kagent`: a CLI built with the root Makefile's `LDFLAGS` reports the version, commit and build date, where `main` reports `dev`, `none` and `unknown`.

2026-10-27: kagent-dev/kagent#3376 merged on 2026-10-26 (`7e02b577`), after `96c40300`; the commit drops at the first re-pin onto a commit containing `7e02b577`.
