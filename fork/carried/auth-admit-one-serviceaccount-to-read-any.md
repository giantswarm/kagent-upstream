---
id: auth-admit-one-serviceaccount-to-read-any
fork: kagent
theme: identity-authorizer
category: gs-specific
status: carried
commits:
  - 'feat(auth): admit one ServiceAccount to read any Session by ID'
upstream:
  outcome: none
  refs: [giantswarm/kagent-upstream#735]
  exit: 'class 2 upstream-has-it. Mechanism: ListSessions with all_creators (proto/kagent/api/v1/sessions.proto:90 on kagent-dev/kagent main 96c40300) returns every Session with its Agent reference, and the authorizer decides who may list all creators; the notifier''s ServiceAccount is granted that list through the out-of-tree binary''s authorizer and one ClusterRole (list on sessions.api.kagent.dev), and resolves a Session''s Agent from the list, cached. No session reader is needed. First step, target the Slack gateway and the platform chart: replace the GetSession call by the list lookup and render the ClusterRole. Test: the gateway''s refusals unit test on the list-based mapping, and the platform lab''s coding test stage that expects a refusal notice. Dependencies: auth-accept-a-serviceaccount-token-and (the authorizer that grants the list). Exit: the notifier change is on the line; the patch drops at the following re-pin. Re-check: when the binary is on the line. Owner: unassigned. The egress refusal series carries only the atespace and the actor name, so no metric-based mapping exists'
depends_on: [auth-accept-a-serviceaccount-token-and]
verified: 2026-10-20
---
# feat(auth): admit one ServiceAccount to read any Session by ID

## Change

`feat(auth): admit one ServiceAccount to read any Session by ID`

## Reason

a platform service that receives a Session ID (the egress refusal notifier, giantswarm/klaus-gateway) maps the Session to its Agent, but `SessionService/GetSession` reads a Session only for its creator, a person, unless the call is the controller's own, so a ServiceAccount's read is always `NotFound` (giantswarm/kagent-upstream#735). `KAGENT_AUTHORIZATION_SESSION_READER_SERVICE_ACCOUNT` (chart `controller.authorization.sessionReaderServiceAccount`, `<namespace>/<name>`, empty by default, which admits none) names the session reader: a call the API server authenticates as that ServiceAccount by TokenReview (`fork/carried/auth-accept-a-serviceaccount-token-and.md`) is admitted to `SessionService/GetSession`, reads any Session by ID, and is refused on every other gRPC method (`PermissionDenied`), on `/mcp` and on the A2A HTTP endpoint (`401`), whatever its RBAC; a share never widens its read. A person's token or an agent's call that names the same user, every other ServiceAccount and the noop mode are unchanged; the controller refuses to start with a reader outside `trusted-proxy` authentication and `kubernetes` authorization. Docs: `docs/architecture/oidc-proxy-authentication.md`

## History

fork-only, never upstream on its own: it serves a Giant Swarm component, and it rests on the ServiceAccount token review and the Kubernetes authorizer, which upstream has not taken

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 2: upstream's ListSessions with all_creators replaces the session reader; no filing.
