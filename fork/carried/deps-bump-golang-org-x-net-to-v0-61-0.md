---
id: deps-bump-golang-org-x-net-to-v0-61-0
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(deps): bump golang.org/x/net to v0.61.0 for two HTTP/2 CVEs'
upstream:
  outcome: drops-at-repin
  refs: [kagent-dev/kagent#3229]
  exit: 'drops at the next re-pin: upstream main requires v0.61.0 since kagent-dev/kagent#3229'
  class: 2
  destination: 'kagent-dev/kagent#3229 (x/net v0.61.0 on upstream main)'
  first_step: 'none; the first re-pin'
  source: n/a
  test: 'go/go.mod:68 equals upstream main'
  recheck: 'at the re-pin'
  owner: unassigned
  draft: ""
  decision: issuecomment-6301335159
depends_on: []
verified: 2026-10-20
---
# fix(deps): bump golang.org/x/net to v0.61.0 for two HTTP/2 CVEs

## Change

`fix(deps): bump golang.org/x/net to v0.61.0 for two HTTP/2 CVEs`

## Reason

CVE-2026-97032 (GO-2026-6617) and CVE-2026-78663 (GO-2026-6612) were read as fixed in golang.org/x/net v0.61.0; both are fixed in v0.60.0 (the previous commit) and no advisory affects v0.60.0. The line has no nancy step: its gates are the trivy image scan and `scripts/fork/govulncheck.sh`, both clean at v0.60.0; the golang.org/x modules x/net v0.61.0 requires came along

## History

to file (upstream main is on x/net v0.59.0); dropped at the re-pin onto an upstream that has it

2026-10-20: kagent-dev/kagent `main` requires x/net v0.61.0, so the next re-pin onto it drops this commit.

2026-10-20: decided in the version-pins theme review (decision issuecomment-6301335159): class 2 upstream-has-it: upstream `main` requires x/net v0.61.0 (go/go.mod:68, dependabot kagent-dev/kagent#3229, 2026-10-15); the commit drops at the first re-pin and the CVE reason is corrected.
