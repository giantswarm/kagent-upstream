---
id: adk-keep-credential-headers-out-of-the
fork: kagent
theme: credential-injection
category: bugfix
status: carried
commits:
  - 'fix(adk): keep credential headers out of the persisted session state'
upstream:
  outcome: to-file
  refs: []
  exit: "PR after the upstream ack; re-check each re-pin"
depends_on: []
verified: 2026-10-20
---
# fix(adk): keep credential headers out of the persisted session state

## Change

`fix(adk): keep credential headers out of the persisted session state`

## Reason

the Python ADK handed a turn its request headers as persisted session state, so an actor-local SQLite session store kept every caller's `Authorization` header at rest in the session row and in each turn's event, and with it in the actor's durable directory and its snapshots. Credential-bearing headers now reach the turn through ADK `temp:` state only, and `LocalSessionService` removes the ones a store written before holds, once, at its first use. The Go ADK keeps request headers in the call context and persists none

## History

to file — prepared in the fork: branch `upstream/adk-credential-headers-not-persisted`

2026-10-20, decided on internal issue 38652, comment 6301541135: known defect, private report drafted 2026-10-20 on internal issue 38652; PR after the ack from the branch `upstream/adk-credential-headers-not-persisted` @ `31824c0c5`; owner unassigned; draft: internal issue 38652, comment 6301543743.
