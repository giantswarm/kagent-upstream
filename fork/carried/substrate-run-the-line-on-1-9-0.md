---
id: substrate-run-the-line-on-1-9-0
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'chore(substrate): run the line on 1.9.0, the re-pin onto v0.5.0-alpha2'
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
depends_on: []
verified: 2026-10-20
---
# chore(substrate): run the line on 1.9.0, the re-pin onto v0.5.0-alpha2

## Change

`chore(substrate): run the line on 1.9.0, the re-pin onto v0.5.0-alpha2`

## Reason

upstream `main@92dd6ca` builds against kagent-dev/substrate `v0.5.0-alpha2` (`pkg/postgressetup`, the ateom/actor identity split); the Substrate line's `1.9.0` is that release plus the line's patches, so the `go/go.mod` replace target and the charts' Substrate dependency move to it together. The line's chart version is set ahead of `LDFLAGS`, where upstream derives it from a kagent-dev replace target the line does not have

## History

fork-only: the pin is the line's own

2026-10-20: decided in the version-pins theme review (decision issuecomment-6301335159): class 1 drop: superseded by substrate-run-the-line-on-1-12-1; squashed into the current pin at the next re-pin, this file deleted then (carried.sh needs it until).
