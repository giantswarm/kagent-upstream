---
id: auth-accept-a-serviceaccount-token-and
fork: kagent
theme: identity-authorizer
category: gs-specific
status: carried
commits:
  - 'feat(auth): accept a ServiceAccount token and list every creator''s Sessions under RBAC in kubernetes mode'
  - 'refactor(auth): build the controller''s Kubernetes client once in kubernetes mode'
upstream:
  outcome: fork-only
  refs: [giantswarm/kagent-upstream#633, kagent-dev/kagent#3282, kagent-dev/kagent#2075]
  exit: 'class 3 platform + 4 upstream issue. Destination: the same out-of-tree controller binary (see auth-authorize-each-caller-against-kubernetes-rbac): its authenticator verifies a ServiceAccount token with a TokenReview carrying the controller''s audience (short result cache, allowlist of callers), and all_creators is the SessionAllCreators check upstream already asks the authorizer (go/core/internal/service/session/service.go:222 on kagent-dev/kagent main 96c40300). First step, target kagent-dev/kagent: issue "verify bearer tokens against configured issuers and audiences" (#3282''s verifier generalised to configured issuers: the people''s OIDC issuer next to the actor issuer; ServiceAccount tokens stay on TokenReview; asked for on #2075), filed now that #3282 is merged (2026-10-21); new branch. Test: platform lab tenants edge step E8 (a projected token of the manager''s ServiceAccount lists every creator''s Sessions; a person''s token is still refused all_creators, step E6). Dependencies: auth-authorize-each-caller-against-kubernetes-rbac. Exit: the binary is on the line; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: [auth-authorize-each-caller-against-kubernetes-rbac]
verified: 2026-10-22
---
# feat(auth): accept a ServiceAccount token and list every creator's Sessions under RBAC in kubernetes mode

## Change

`feat(auth): accept a ServiceAccount token and list every creator's Sessions under RBAC in kubernetes mode` and `refactor(auth): build the controller's Kubernetes client once in kubernetes mode`

## Reason

the platform's workspace manager (giantswarm/workspace-manager) lists every creator's Sessions to clean up their directories on the workspace volume, with `ListSessions{all_creators: true}` and a projected ServiceAccount token of audience `kagent`; the Kubernetes authorizer (`fork/carried/auth-authorize-each-caller-against-kubernetes-rbac.md`) refuses that token, which has no user ID claim, and refuses `all_creators` for everyone, so the platform could not turn the mode on. In the kubernetes mode a direct call whose token's `sub` names a ServiceAccount is authenticated with a TokenReview for `KAGENT_AUTHORIZATION_SERVICE_ACCOUNT_TOKEN_AUDIENCE` (chart `controller.authorization.serviceAccountTokenAudience`, default `kagent`, never empty) and reviewed as the user and groups the API server returns, unprefixed; a token the API server does not authenticate as a ServiceAccount for that audience is refused; `all_creators` is reviewed as `list` on `sessions.api.kagent.dev` cluster-wide, which a ClusterRole grants the ServiceAccount and a person normally lacks (a wildcard ClusterRole such as `cluster-admin` grants it too); the chart grants the controller `create` on `tokenreviews` in that mode. A person's token, an agent's call and the noop mode are unchanged. The ClusterRole for the workspace manager's ServiceAccount belongs to the agent-platform charts

## History

fork-only, never upstream: it serves a Giant Swarm component, and it rests on the Kubernetes authorizer, which upstream has not taken

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 3 in the out-of-tree binary, with a class 4 issue on trusted issuers after kagent-dev/kagent#3282.

2026-10-22: amended in the theme decision (amendment 8, internal): TokenReview stays for machine callers, now with the controller audience and an allowlist; the trusted-issuers issue covers people's tokens.
