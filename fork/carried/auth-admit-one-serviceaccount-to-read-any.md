---
id: auth-admit-one-serviceaccount-to-read-any
fork: kagent
theme: identity-authorizer
category: gs-specific
status: carried
commits:
  - 'feat(auth): admit one ServiceAccount to read any Session by ID'
  - "fix(session): the session reader reads a deleted Session's tombstone"
upstream:
  outcome: fork-only
  refs: [giantswarm/kagent-upstream#735, giantswarm/klaus-gateway#512, kagent-dev/kagent#3299]
  exit: 'class 3 platform for a live Session, class 6 fork-only for a deleted one. Live: the notifier authenticates to the out-of-tree binary as a machine (see auth-accept-a-serviceaccount-token-and) and is granted get on sessions.api.kagent.dev only, the one Session-to-Agent lookup it needs; no list of every creator. Deleted: a refusal by a Session deleted before the notifier reads it (giantswarm/klaus-gateway#512, a short scheduled or one-shot agent) still needs the tombstone read, and the reader stays for it. First step, target the Substrate line: issue "name the actor''s labels on the egress refusal series" (today the series carries only the atespace and the actor name), so kagent labels each actor with its Agent and the notifier reads no Session at all. Test: the gateway''s refusals unit test with a Session gone before the read, and the platform lab''s coding test stage that expects a refusal notice. Dependencies: auth-accept-a-serviceaccount-token-and. Exit: the refusal series names the Agent and the notifier reads it; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned'
depends_on: [auth-accept-a-serviceaccount-token-and]
verified: 2026-10-22
---
# feat(auth): admit one ServiceAccount to read any Session by ID

## Change

`feat(auth): admit one ServiceAccount to read any Session by ID`

## Reason

a platform service that receives a Session ID (the egress refusal notifier, giantswarm/klaus-gateway) maps the Session to its Agent, but `SessionService/GetSession` reads a Session only for its creator, a person, unless the call is the controller's own, so a ServiceAccount's read is always `NotFound` (giantswarm/kagent-upstream#735). `KAGENT_AUTHORIZATION_SESSION_READER_SERVICE_ACCOUNT` (chart `controller.authorization.sessionReaderServiceAccount`, `<namespace>/<name>`, empty by default, which admits none) names the session reader: a call the API server authenticates as that ServiceAccount by TokenReview (`fork/carried/auth-accept-a-serviceaccount-token-and.md`) is admitted to `SessionService/GetSession`, reads any Session by ID, and is refused on every other gRPC method (`PermissionDenied`), on `/mcp` and on the A2A HTTP endpoint (`401`), whatever its RBAC; a share never widens its read. A person's token or an agent's call that names the same user, every other ServiceAccount and the noop mode are unchanged; the controller refuses to start with a reader outside `trusted-proxy` authentication and `kubernetes` authorization. Docs: `docs/architecture/oidc-proxy-authentication.md`

A Session its creator deleted still answers the reader with its tombstone (`RUNTIME_STATE_DELETED`, the Agent it ran), and no other caller: the reader is handed a Session ID after the fact, by an egress refusal, and a short-lived Session is often gone by then, so the refusal could not be attributed to its Agent (giantswarm/klaus-gateway#512).

## History

fork-only, never upstream on its own: it serves a Giant Swarm component, and it rests on the ServiceAccount token review and the Kubernetes authorizer, which upstream has not taken

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 2: upstream's ListSessions with all_creators replaces the session reader; no filing.

2026-10-22: the T9 class 2 placement covers live Sessions only: the tombstone read for giantswarm/klaus-gateway#512 needs a deleted Session, which ListSessions does not return, so the row is fork-only until the egress refusal series names the Agent.

2026-10-22: amended in the theme decision (amendment 8, internal): the live read narrows to get on sessions through the binary; the all-creators list is dropped; the tombstone stays until the refusal series names the Agent.
