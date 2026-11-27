---
id: ci-fork-keep-the-e2e-image-builds-go
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'ci(fork): keep the e2e image builds'' Go build cache between runs'
upstream:
  outcome: fork-only
  refs: []
  exit: 'drops when the line''s e2e job runs on runners that keep BuildKit''s cache between runs, as upstream''s Blacksmith runners do'
depends_on: [ci-fork-split-the-e2e-tests-in-two]
verified: 2026-11-26
---
# ci(fork): keep the e2e image builds' Go build cache between runs

## Change

`ci(fork): keep the e2e image builds' Go build cache between runs`

## Reason

The e2e job builds seven images before it installs kagent, and on a GitHub-hosted runner every build compiles Go from an empty `/root/.cache/go-build` cache mount: the `go build` stages took 320 to 412 s each, about nine of the ten minutes of `Install Kagent`. Upstream's Blacksmith runners keep that cache between runs; the line's runners do not. `reproducible-containers/buildkit-cache-dance` (pinned by commit) moves the mount in and out of an Actions cache, saved by one leg of each push to `giantswarm` and restored everywhere else. Pull requests no longer save their own `e2e-go` copy, which only their reruns could read and which kept the repository's 10 GB of caches full.

## History

2026-11-26: carried; fork-only, nothing to file upstream.
