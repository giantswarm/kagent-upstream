---
id: deps-build-with-go-1-27-4
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(deps): build with Go 1.27.4'
upstream:
  outcome: to-file
  refs: []
  exit: 'Drop when the upstream Go directive requires 1.27.4 or newer; re-check at each re-pin.'
depends_on: [deps-build-with-go-1-27-2]
verified: 2026-11-13
---
# Build with Go 1.27.4

## Change

Raise the Go directive from 1.27.3 to 1.27.4. CI setup-go, the Makefile
and module-aware image builds consume this minimum toolchain version.

## Reason

GO-2026-6859 is a net/http HTTP/1 server crash caused by a MaxBytesReader
header race, reached from kagent code in the module scan. The Go vulnerability
database identifies 1.27.4 as the fixed release on the 1.27 line:
https://vuln.go.dev/ID/GO-2026-6859.json. No scanner exclusion is added.

## Upstream

to file — prepared in the fork: branch `upstream/go-1-27-4`
(`3f7f2968e`, DCO-signed), based on the mirrored upstream pin `92dd6caad`.
The commit changes only `go/go.mod`, from the pin's Go 1.27.0 to 1.27.4,
and contains no fork references. Filing awaits a human maintainer.
Upstream search on 2026-11-13: all-state pull requests matching `1.27.4`
and issues matching `GO-2026-6859` returned no results in `kagent-dev/kagent`.
The current mirror `main@347acbe55` still requires Go 1.27.3.
No upstream issue or pull request was opened.

## History

The module scan on commit 5bdab03cc reported GO-2026-6859 under Go 1.27.3.
The toolchain bump passed govulncheck and scan-ok on `ff652f9b8`
([security workflow](https://github.com/giantswarm/kagent-upstream/actions/runs/43005611094)).
CI validation of subsequent review fixes is owned by their new head; successful
live agentlab and conversation-upgrade proof remains a separate pre-merge gate.
