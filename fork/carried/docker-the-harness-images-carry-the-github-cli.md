---
id: docker-the-harness-images-carry-the-github-cli
fork: kagent
theme: harness-variety
category: feature
status: dropped
commits:
  - 'feat(docker): the Harness images carry the GitHub CLI'
  - 'fix(docker): gh and glab leave the Harness images for the github and gitlab toolchains'
upstream:
  outcome: drops-at-repin
  refs: [kagent-dev/kagent#3192]
  exit: the next re-pin folds the carry and its removal out
depends_on: [kagent-dev/kagent#3192]
verified: 2026-10-22
---
# feat(docker): the Harness images carry the GitHub CLI

## Change

`feat(docker): the Harness images carry the GitHub CLI`

## Reason

a coding agent in a workspace Session works its clone with git and `gh` (`gh pr create`, `gh pr view`, `gh run list`), and neither the Go runtime image nor the Claude harness image carried `gh` (giantswarm/kagent-upstream#288). Both install Alpine's `github-cli` at the one version in `go/github-cli.version`, bind-mounted into the `apk add` step; the Claude harness image moves to `alpine:3.24`, the release `go/Dockerfile` uses, so the pin resolves in both. No credential is configured. Renovate tracks the pin through one `custom.regex` manager in `renovate.json`, the only manager the line runs

## History

to file (upstream's images carry no `gh`; if upstream declines it for the Go ADK image, the row becomes fork-only)

dropped: the `github` and `gitlab` toolchains (giantswarm/agent-toolchains) carry `gh` and `glab` as a Session's tool images beside the Harness image, so `fix(docker): gh and glab leave the Harness images for the github and gitlab toolchains` removes them from the Go runtime and Claude harness images; the next re-pin folds the carried commits and their removal out of the line
