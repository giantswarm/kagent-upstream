---
id: deps-build-with-go-1-27-2
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(deps): build with Go 1.27.2 and golang.org/x/net v0.60.0'
upstream:
  outcome: drops-at-repin
  refs: [kagent-dev/kagent#3304, kagent-dev/kagent#3159, kagent-dev/kagent#2535]
  exit: 'drops at the re-pin onto a commit containing 7906c6d6d (kagent-dev/kagent#3304: go 1.27.3 in go/go.mod, which the CI jobs, the Makefile''s GOTOOLCHAIN and the image builds read); the line carries the same directive since 2026-10-21'
  class: 2
  destination: 'kagent-dev/kagent#3304 (7906c6d6d, on upstream main)'
  first_step: 'none; the toolchain-line pull request drafted on 2026-10-20 is withdrawn'
  source: n/a
  test: 'scripts/fork/govulncheck.sh (the image-scan workflow) is green on the re-pin'
  recheck: 'at the re-pin'
  owner: unassigned
  draft: ""
  decision: issuecomment-6301335159
depends_on: [go-lint-with-golangci-lint-v2-14-0]
verified: 2026-10-23
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

2026-10-23: upstream has it: kagent-dev/kagent#3304 (merged 2026-10-21, `7906c6d6d`) moved the `go` directive to 1.27.3 and `.custom-gcl.yml` to golangci-lint v2.14.0, the shape the line carries since 2026-10-21; the toolchain-line pull request drafted on 2026-10-20 is withdrawn, nothing to file. Class 2: drops at the re-pin onto a commit containing `7906c6d6d`. Upstream moves its Go version by hand: Dependabot cannot change the `go` or `toolchain` directive (dependabot/dependabot-core#13520) and kagent-dev/kagent#693 (Renovate) was closed, so the next Go patch is another hand-written pull request there, outside this row.
