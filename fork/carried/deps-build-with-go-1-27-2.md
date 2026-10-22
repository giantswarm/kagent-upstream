---
id: deps-build-with-go-1-27-2
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(deps): build with Go 1.27.2 and golang.org/x/net v0.60.0'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3159, kagent-dev/kagent#2535, agentgateway/agentgateway#4063]
  exit: 'interim on the line: the go directive at 1.27.3 (chore(deps): build with go 1.27.3 for GO-2026-6809, 2026-10-21), which CI, the Makefile''s GOTOOLCHAIN and the images read; upstream PR on kagent-dev/kagent (toolchain go1.27.3 under go 1.27.0, Makefile fallback, .custom-gcl.yml at golangci-lint v2.14.0); the line mirrors the upstream shape once the Substrate line''s go directive returns to 1.27.0; drops at the re-pin onto the merge, or at the re-pin that brings upstream''s own bump'
  class: 5
  destination: kagent-dev/kagent
  first_step: 'interim on the line done (go directive 1.27.3); one PR on kagent-dev/kagent: a toolchain go1.27.3 line under go 1.27.0, the Makefile reading it with the go directive as fallback, and .custom-gcl.yml at golangci-lint v2.14.0; then the line mirrors it once giantswarm/substrate:require-golang-org-x-net-v0-61 is reshaped'
  source: 'new branch upstream/toolchain-go1-27-3 on giantswarm/kagent-upstream, base mirror main at or past 2a50a8be'
  test: 'make govulncheck exits 0; govulncheck -mode binary on core/cmd/controller and core/cli/cmd/kagent shows no stdlib finding (go1.27.0 control: 11); make lint green; go version -m bin/golangci-lint-custom shows x/tools v0.50.0'
  recheck: 2026-11-17
  owner: unassigned
  draft: issuecomment-6301335589
  decision: issuecomment-6301335159
depends_on: [go-lint-with-golangci-lint-v2-14-0, giantswarm/substrate:require-golang-org-x-net-v0-61]
verified: 2026-10-22
---
# fix(deps): build with Go 1.27.2 and golang.org/x/net v0.60.0

## Change

`fix(deps): build with Go 1.27.2 and golang.org/x/net v0.60.0`

## Reason

the October Go security release (GO-2026-6599 to GO-2026-6617: net/http HTTP/1 and HTTP/2, html/template, crypto/tls, net/textproto) is reached from kagent code and fails `scripts/fork/govulncheck.sh` (the `image-scan` workflow) on every pull request; fixed in Go 1.27.2 and, for the HPACK encoder race, x/net v0.60.0; Go 1.27.3 adds the crypto/x509 fixes. The `go` directive drives the CI toolchain (`go-version-file`) and the tools images (`TOOLS_GO_VERSION`); the runtime image builds on `chainguard/go:latest`

## History

to file (upstream main is on Go 1.27.0 and x/net v0.59.0); dropped at the re-pin onto an upstream that has both

2026-10-20: decided in the version-pins theme review (decision issuecomment-6301335159): class 5 upstream PR, uncapped (under 100 lines, one concern; precedents kagent-dev/kagent#3159, #2535, #2235). Upstream `main` at `2a50a8be` builds on go 1.27.0 (go/go.mod:3 drives the six CI jobs' `go-version-file`, `GOTOOLCHAIN` and `TOOLS_GO_VERSION`, Makefile:99-100,122); `govulncheck -mode binary` on its controller built with go1.27.0 shows 11 standard-library findings, none with go1.27.3. The upstream shape is a `toolchain go1.27.3` line (main module only, as agentgateway#4063 did on 2026-10-20) with the Makefile reading it, not a `go` directive bump, which would raise every importer's minimum. Interim on the line first: the toolchain line beside the forced `go 1.27.2` so the binaries carry 1.27.3; the upstream shape is mirrored once the Substrate line's go directive returns to 1.27.0.

2026-10-22: the interim step is the go directive at 1.27.3 for GO-2026-6809 (crypto/x509), not a toolchain line beside 1.27.2; the upstream PR and its shape are unchanged.
