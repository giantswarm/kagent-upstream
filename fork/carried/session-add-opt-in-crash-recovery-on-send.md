---
id: session-add-opt-in-crash-recovery-on-send
fork: kagent
theme: session-lifecycle
category: pin
status: carried
commits:
  - 'feat(session): add opt-in crash recovery on send'
  - 'fix(ui): cancel the task when Stop is pressed before it is named'
  - '[backport] fix(ui): bump nginx to 1.31.9 for HTTP/2 CVEs'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3399, kagent-dev/kagent#3623, kagent-dev/kagent#3732]
  exit: 'drops by patch id at the re-pin onto a release/v1.0.x head or a release that contains e59d4892b, 298d52e6a and c86529b07'
depends_on: []
verified: 2026-11-26
---
# feat(session): add opt-in crash recovery on send

## Change

`cherry-pick -x` of three upstream commits onto kagent v1.0.1: `feat(session): add opt-in crash recovery on send` (kagent-dev/kagent#3399, `e59d4892b`), its prerequisite `fix(ui): cancel the task when Stop is pressed before it is named` (kagent-dev/kagent#3623, `298d52e6a`) and `[backport] fix(ui): bump nginx to 1.31.9 for HTTP/2 CVEs` (kagent-dev/kagent#3732, `c86529b07`, already on `release/v1.0.x`).

## Reason

The line's sessions revert and resume a crashed runtime on the next send. v1.0.1 has `RevertSession` and crash recovery only when a client asks (kagent-dev/kagent#3395); the automatic path on send is kagent-dev/kagent#3399, merged on `main` after v1.0.1. It replaces the line's own revert-on-send code. `config.sessions.automaticRecovery` turns it on; the platform sets it. The backport keeps `ErrActiveTurn` only from `errors.go` and calls `NewActorWorkflow` without the meter provider that later `main` commits add.

## History

2026-11-26: picked for the re-pin onto kagent v1.0.1.
