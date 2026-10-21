---
id: egress-actor-jwt-hosts-only-during-a-turn
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(egress): actor-JWT hosts only during a turn'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3156]
  exit: with the Session caller-credential series
depends_on: [egress-the-actor-jwt-on-the-installations-providers]
verified: 2026-10-20
---
# feat(egress): actor-JWT hosts only during a turn

## Change

`feat(egress): actor-JWT hosts only during a turn`

## Reason

the egress gateway hands a Session's Actor its JWT on every request to a host whose provider verifies that JWT (muster on the Giant Swarm platform), whether or not the Session has a running turn: only Substrate suspending the Actor once a turn settles kept a process in the sandbox from calling it between turns, and a runtime that keeps its sandbox running would not. Every policy kagent writes outside a turn (create, repoint, the move back after a lost race) now leaves such a host off, every origin of it the Agent lists too, so the gateway refuses the Actor there with `403`. The A2A gateway opens the host after it reserves a turn's dispatch and before it dials the runtime (an always-landing `UpdateActorEgressPolicy`; a failed opening refuses the send, retryable). The turn's end closes it: the boundary's idle work before Pause or Quiesce, the stalled turn sweep, a crashed turn's interruption, and a dispatch revoked because the runtime never took the input; each close writes behind the policy version it read while the turn still held the session, so a later turn's opening wins over a late close. `TestTurnEgressClosesBehindItsFence`, `TestIdleWorkClosesTheTurnsEgressBeforeTheRuntimeStops`, `TestStalledTurnSweepClosesTheTurnsEgress`, `TestGatewayOpensTheTurnsEgressAndClosesItForAnUnacceptedInput`, `TestSessionEgressPolicyBindsTheActorJWTOfAProviderThatVerifiesIt`

## History

to file — prepared in the fork once the Session's actor-JWT providers are upstream (kagent-dev/kagent#3156): the turn bound applies to the `actor_jwt` entries of `Session.egress` in the upstream form, filed with or after the Session caller-credential series.
