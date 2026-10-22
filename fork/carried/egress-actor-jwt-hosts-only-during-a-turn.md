---
id: egress-actor-jwt-hosts-only-during-a-turn
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(egress): actor-JWT hosts only during a turn'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3263]
  exit: "kagent-dev/kagent ships a per-turn phase for egress policy compilers (kagent-dev/kagent#3263's EgressPolicyCompilers run when a turn opens and when it ends): the turn bound becomes the platform compiler's and the file drops at that re-pin; re-decided 2026-11-17; re-check each re-pin"
depends_on: [egress-the-actor-jwt-on-the-installations-providers, kagent-dev/kagent#3263]
verified: 2026-10-22
---
# feat(egress): actor-JWT hosts only during a turn

## Change

`feat(egress): actor-JWT hosts only during a turn`

## Reason

the egress gateway hands a Session's Actor its JWT on every request to a host whose provider verifies that JWT (muster on the Giant Swarm platform), whether or not the Session has a running turn: only Substrate suspending the Actor once a turn settles kept a process in the sandbox from calling it between turns, and a runtime that keeps its sandbox running would not. Every policy kagent writes outside a turn (create, repoint, the move back after a lost race) now leaves such a host off, every origin of it the Agent lists too, so the gateway refuses the Actor there with `403`. The A2A gateway opens the host after it reserves a turn's dispatch and before it dials the runtime (an always-landing `UpdateActorEgressPolicy`; a failed opening refuses the send, retryable). The turn's end closes it: the boundary's idle work before Pause or Quiesce, the stalled turn sweep, a crashed turn's interruption, and a dispatch revoked because the runtime never took the input; each close writes behind the policy version it read while the turn still held the session, so a later turn's opening wins over a late close. `TestTurnEgressClosesBehindItsFence`, `TestIdleWorkClosesTheTurnsEgressBeforeTheRuntimeStops`, `TestStalledTurnSweepClosesTheTurnsEgress`, `TestGatewayOpensTheTurnsEgressAndClosesItForAnUnacceptedInput`, `TestSessionEgressPolicyBindsTheActorJWTOfAProviderThatVerifiesIt`

## History

to file — prepared in the fork once the Session's actor-JWT providers are upstream (kagent-dev/kagent#3156): the turn bound applies to the `actor_jwt` entries of `Session.egress` in the upstream form, filed with or after the Session caller-credential series.

2026-10-22, decided on internal issue 38652: class 4 upstream issue, carried fork-only until it lands; upstream has no per-turn seam (an egress policy compiler runs once per revision), and without the bound a sandbox process calls an actor-JWT host as the person while its runtime waits for input and until quiescence suspends it; destination a kagent-dev/kagent issue asking for a per-turn phase of `EgressPolicyCompilers` (the gateway runs compilers when it opens a turn and when the turn ends, each write fenced on the policy version it read), this file's commit as the reference implementation; first step that issue, then a pull request once the shape is agreed; test `TestTurnEgressClosesBehindItsFence`, `TestIdleWorkClosesTheTurnsEgressBeforeTheRuntimeStops`, `TestStalledTurnSweepClosesTheTurnsEgress`, `TestGatewayOpensTheTurnsEgressAndClosesItForAnUnacceptedInput`; depends on no theme; re-check 2026-11-17; owner Quentin.
