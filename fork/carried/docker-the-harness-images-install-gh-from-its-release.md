---
id: docker-the-harness-images-install-gh-from-its-release
fork: kagent
theme: harness-variety
category: pin
status: dropped
commits:
  - 'fix(docker): the Harness images install gh from its release, at 2.102.0'
  - 'fix(ci): accept gh 2.102.0''s Go findings until its next release'
  - 'fix(docker): install gh 2.102.1, built on Go 1.27.3'
upstream:
  outcome: drops-at-repin
  refs: []
  exit: the next re-pin folds the carry and its removal out
depends_on: [docker-the-harness-images-carry-the-github-cli]
verified: 2026-10-22
---
# fix(docker): the Harness images install gh from its release, at 2.102.0

## Change

`fix(docker): the Harness images install gh from its release, at 2.102.0`, `fix(ci): accept gh 2.102.0's Go findings until its next release`, `fix(docker): install gh 2.102.1, built on Go 1.27.3`

## Reason

a coding agent in a Harness attaches screenshots and recordings to its issue and pull request comments with `gh … comment --attach`, which `gh` has from 2.99.0, and Alpine 3.24 ships `github-cli` 2.97.0 (giantswarm/kagent-upstream#572). The Go runtime and Claude harness images install the GitHub CLI release tarball through `go/install-github-cli.sh`, verified against the sha256 per architecture pinned in `go/github-cli.version` (`<tag> <arch> <sha256>`); Renovate's `github-release-attachments` datasource on `cli/cli` moves the tag and both checksums in one change, off repology. gh v2.102.0 is built with Go 1.27.1 and x/net v0.58.0: `.github/image-scan.rego` accepts its four HIGH findings at those versions until 2026-11-14 (giantswarm/kagent-upstream#575). gh v2.102.1 is built with Go 1.27.3 and x/net v0.60.0, which also fixes CVE-2026-111943 (crypto/x509, HIGH) that failed every scan of v2.102.0; the pin moves to it and the exception is removed

## History

to file — the first commit folds into the upstream-ready commit of `feat(docker): the Harness images carry the GitHub CLI` when that is prepared; the second is fork-only and its exception left with the third, the pin at v2.102.1

dropped: the `github` and `gitlab` toolchains (giantswarm/agent-toolchains) carry `gh` and `glab` as a Session's tool images beside the Harness image, so `fix(docker): gh and glab leave the Harness images for the github and gitlab toolchains` removes them from the Go runtime and Claude harness images; the next re-pin folds the carried commits and their removal out of the line
