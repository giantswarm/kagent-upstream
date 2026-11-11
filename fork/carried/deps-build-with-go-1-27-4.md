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
verified: 2026-11-11
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

## History

The module scan on commit 5bdab03cc reported GO-2026-6859 under Go 1.27.3.
The toolchain bump awaits CI validation; no upstream issue or PR was opened.
