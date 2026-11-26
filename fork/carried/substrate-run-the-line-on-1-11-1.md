---
id: substrate-run-the-line-on-1-11-1
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'chore(substrate): run the line on 1.11.1 for the atelet terminate fix'
upstream:
  outcome: fork-only
  refs: []
  exit: 'squashed into the current pin at the next re-pin; this file is deleted then'
  class: 1
  destination: ""
  first_step: 'none; squashed into the current pin at the next re-pin'
  source: n/a
  test: 'scripts/fork/carried.sh --check after the squash'
  recheck: 'at the re-pin'
  owner: unassigned
  draft: ""
  decision: issuecomment-6301335159
depends_on: [giantswarm/substrate:atelet-terminates-actor-whose-restore-failed]
verified: 2026-10-20
---
# chore(substrate): run the line on 1.11.1 for the atelet terminate fix

## Change

`chore(substrate): run the line on 1.11.1 for the atelet terminate fix`

## Reason

the Substrate line's `1.11.1` carries atelet's terminate of an Actor whose sandbox died during `runsc restore` (giantswarm/substrate#436): on `1.9.0` every later terminate failed with `failed to read sandbox record`, so a crashed Actor could be neither reverted nor deleted and the Session's revert-and-resume (giantswarm/kagent-upstream#580) stalls. The `go/go.mod` replace target and the Makefile's `SUBSTRATE_VERSION` move to it together; the ate-api contract is unchanged from `1.9.0` (v0.5.0-alpha2), the controller sets no `MintActorJWTRequest` field and writes no egress policy through `kubectl-ate`

## History

fork-only: the pin is the line's own

2026-10-20: decided in the version-pins theme review (decision issuecomment-6301335159): class 1 drop: superseded by substrate-run-the-line-on-1-12-1; squashed into the current pin at the next re-pin, this file deleted then (carried.sh needs it until).
