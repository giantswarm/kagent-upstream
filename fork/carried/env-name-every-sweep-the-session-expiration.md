---
id: env-name-every-sweep-the-session-expiration
fork: kagent
theme: session-lifecycle
category: bugfix
status: carried
commits:
  - 'fix(env): name every sweep the session expiration poll interval drives'
upstream:
  outcome: fork-only
  refs: [giantswarm/kagent-upstream#362]
  exit: 'leaves with the sweeps it names: upstream has no deleted-Agent sweep, no stalled-turn worker and no share expiry sweep, so the wording drops at the re-pin where upstream carries them, or folds into their upstream branches when those are filed'
depends_on: [session-delete-the-sessions-of-a-deleted-agent, session-delete-expired-shares-on-the-expiration-sweep, session-end-a-working-turn-whose-runtime-stopped]
verified: 2026-10-26
---
# fix(env): name every sweep the session expiration poll interval drives

## Change

`fix(env): name every sweep the session expiration poll interval drives`

## Reason

`KAGENT_SESSION_EXPIRATION_POLL_INTERVAL` was described as the interval of the idle-session and share expiry sweep, but the controller also runs the deleted-Agent sweep (the same `ExpirationWorker` tick) and the stalled-turn worker (`NewStalledTurnWorker`) on it, so an operator tuning it for one sweep changed three (giantswarm/kagent-upstream#362). The description, and `docs/env.md` generated from it, names every sweep the interval drives

## History

fork-only from the start: every sweep the wording names is a carried change of this line
