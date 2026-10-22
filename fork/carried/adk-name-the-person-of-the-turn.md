---
id: adk-name-the-person-of-the-turn
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'fix(adk): name the person of the turn as the request span''s end user'
upstream:
  outcome: fork-only
  refs: []
  exit: 'class 6 fork-only, rides a2agateway-run-a-turn-a-share: the span''s end user differs from x-user-id only on a visitor''s turn through a writable share; upstream''s read-only shares have no visitor turns and its invocation span already names the trusted user (go/adk/pkg/a2a/server/tracing.go:85 on kagent-dev/kagent main 96c40300). First step: none. Test: the fork''s shared-turn e2e checks enduser.id names the visitor. Dependencies: a2agateway-run-a-turn-a-share. Exit: with that row. Re-check: at each re-pin. Owner: unassigned'
depends_on: [a2agateway-run-a-turn-a-share, runtime-name-the-agent-and-the-user]
verified: 2026-10-20
---
# fix(adk): name the person of the turn as the request span's end user

## Change

`fix(adk): name the person of the turn as the request span's end user`

## Reason

the request span took `enduser.id` from `x-user-id`, which names the owner of the session; since a turn a session share authorizes runs in the owner's session (the row "run a shared turn in the instance owner's session" above), the traces of a collaborator's turn named the owner while its model usage named the visitor. The span now takes the person of the turn from `x-kagent-user` and keeps `x-user-id` only for a turn whose caller the gateway did not resolve

## History

to file with "name the agent and the user on every model call", which brings `x-kagent-user` upstream, and the shared-turn row

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 6, rides the writable-share row; nothing to file on its own.
