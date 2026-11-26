---
id: session-end-a-turn-its-crashed-actor-took
fork: kagent
theme: session-lifecycle
category: bugfix
status: dropped
commits:
  - 'fix(session): end a crashed Actor''s turn, also one back at its snapshot'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3255]
  exit: 'read against #3255, then the design issue per family'
depends_on: []
verified: 2026-11-26
---
# fix(session): end a crashed Actor's turn, also one back at its snapshot

## Change

`fix(session): end a crashed Actor's turn, also one back at its snapshot`

## Reason

a turn in flight when its worker goes away (a drain, a replaced pod) crashes the Session's Actor; once a crashed Actor that holds a durable snapshot was no longer a lost runtime, the gateway kept that turn as active work, the restored runtime never finished it, and the conversation refused every send until the stalled-turn sweep (`KAGENT_STALLED_TURN_TIMEOUT`, one hour) ended it. `FailLostTurn` now ends the turn `FAILED` with the crash's message whenever the Actor crashed (`TurnCrash`, which asks again every second for up to 15 s while Substrate still reports the Actor `RUNNING`); it records `RuntimeLost` on the session only when the runtime is lost, and the gateway answers the send with the ended turn. `TestRuntimeLost`, `TestTurnCrash`, `TestFailLostTurn`, `TestGatewayFailsTheTurnWhenRuntimeIsLostAfterTakingTheInput`

## History

to file with the lost-runtime family: upstream has no `RuntimeLost` or `FailLostTurn` to patch, the family goes up as one

2026-10-20: [kagent-dev/kagent#3255](https://github.com/kagent-dev/kagent/pull/3255) (adalundhe, opened 2026-10-16) recovers accepted turns whose Actor crashed and adds `SessionService.RevertSession` on the same session workflow paths; this change is read against it before the family's design issue, the outcome stays to-file.

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), upstream provides it: partial: kagent-dev/kagent#3395 aaf1ca882 (backport #3586) BeginCrashedSessionRecovery fails the interrupted turn with a crash note (go/core/internal/database/session_recovery.go); kagent-dev/kagent#3399 e59d4892b opt-in crash recovery on send is NOT in target. The next landed re-pin removes this file
