---
id: a2agateway-run-a-turn-a-share
fork: kagent
theme: identity-authorizer
category: bugfix
status: carried
commits:
  - 'fix(a2agateway): run a turn a share of the session authorizes in its owner''s session'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3066, kagent-dev/kagent#3189, kagent-dev/kagent#3282]
  exit: 'class 6 fork-only. Reason: the Slack gateway''s thread collaborators send turns through a writable share, and upstream removed writable shares for its GA (kagent-dev/kagent#3189; #3066 closed for that reason). Carried: at the re-pin past #3189 this fork reshapes the patch additively rather than reverting #3189: a writable scope beside upstream''s read-only share (a new value, column or table, never upstream''s removed fields), on the re-pin that theme T13''s conversion targets (a pure-add migration in T13''s reserved goose range, a down-conversion at this row''s exit). The shape keeps the actor policy of kagent-dev/kagent#3282, under which a share token never widens an actor''s access. First step, target kagent-dev/kagent: a new issue on multi-party sessions (linking #3065 and the third-party request there) stating the identity model (the owner''s session, the visitor as the person of the turn with the visitor''s credentials) for every runtime, with per-sender isolation on the harnesses that keep a working directory; never capped. Test: this fork''s shared-turn e2e and the platform lab''s coding test share stage. Dependencies: theme T13, adk-name-the-person-of-the-turn and session-refuse-a-share-on-a-session-whose-harness-runs-code ride with this row. Exit: the upstream issue is decided and its shape is on the line, or the Slack gateway''s fallback is on the line (a collaborator''s turn sent under the thread owner''s token, the collaborator named in message metadata); reviewed at each re-pin; no calendar date. Owner: unassigned. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: []
verified: 2026-10-22
---
# fix(a2agateway): run a turn a share of the session authorizes in its owner's session

## Change

`fix(a2agateway): run a turn a share of the session authorizes in its owner's session`

## Reason

the runtime keys its session on `x-user-id` and the context id, and the runtime dialer forwarded the visitor there, so a turn a session share authorized started an empty session in the conversation's context: a Slack collaborator klaus-gateway lets into a thread (giantswarm/klaus-gateway#356) got an agent with no history (seen on a production installation). The dialer now names the share's owner, as the HTTP A2A handler does for a session share; the visitor stays the person of the turn (`x-kagent-user`) and keeps their own credentials (MCP and model calls). What the runtime keys on `x-user-id` follows the owner too, as before klaus-gateway#356: the long-term memory of the agent and the sessions of its sub-agents

## History

kagent-dev/kagent#3066 (`71b1e7857`), carried as the pull request has it

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 6: writable shares stay in the fork with the #3189 revert carried from the re-pin past it; a new upstream issue on multi-party sessions is the exit, reviewed at each re-pin.
