---
id: go-lint-with-golangci-lint-v2-14-0
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'build(go): lint with golangci-lint v2.14.0 for Go 1.27.2 export data'
upstream:
  outcome: to-file
  refs: []
  exit: 'filed with deps-build-with-go-1-27-2 in the same upstream PR; drops with it'
  class: 5
  destination: kagent-dev/kagent
  first_step: 'in the PR of deps-build-with-go-1-27-2: .custom-gcl.yml version v2.14.0'
  source: 'new branch upstream/toolchain-go1-27-3 on giantswarm/kagent-upstream'
  test: 'make lint green under go1.27.3'
  recheck: 2026-11-17
  owner: unassigned
  draft: issuecomment-6301335589
  decision: issuecomment-6301335159
depends_on: [deps-build-with-go-1-27-2]
verified: 2026-10-20
---
# build(go): lint with golangci-lint v2.14.0 for Go 1.27.2 export data

## Change

`build(go): lint with golangci-lint v2.14.0 for Go 1.27.2 export data`

## Reason

golangci-lint v2.13.0 (x/tools v0.49.0) built with any Go from 1.27.1 fails every package's typecheck with `export data version 5 is greater than maximum supported version 4`: go1.27.1 introduced the unified V5 export data (golang/go `359bec41ae`); v2.14.0 (x/tools v0.50.0) reads it. The kube-api-linter plugin is built from the same golangci-lint source tag

## History

to file with the Go bump above; dropped with it

2026-10-20: decided in the version-pins theme review (decision issuecomment-6301335159): class 5 upstream PR, the same PR as deps-build-with-go-1-27-2. Reproduced on upstream `main` at `2a50a8be`: `go tool golangci-lint custom` under go1.27.3 builds a binary with x/tools v0.49.0 and fails the typecheck the same way, so the upstream PR needs `.custom-gcl.yml` at v2.14.0.
