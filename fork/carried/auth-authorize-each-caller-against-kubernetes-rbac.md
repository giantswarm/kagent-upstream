---
id: auth-authorize-each-caller-against-kubernetes-rbac
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(auth): authorize each caller against Kubernetes RBAC with SubjectAccessReviews'
  - 'fix(auth): refuse unverified identities on the controller'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#2658, kagent-dev/kagent#2710, kagent-dev/kagent#1270, kagent-dev/kagent#3281, kagent-dev/kagent#3282, kagent-dev/kagent#3263, kagent-dev/kagent#3299, giantswarm/kagent-upstream#837]
  exit: 'class 3 platform + 5 upstream PR. Destination: an out-of-tree controller binary that embeds upstream''s app.Run with its own authenticator and authorizer on app.Options (kagent-dev/kagent#2658, #2710), in a Giant Swarm extensions repository shared with the EgressPolicy compiler seam of kagent-dev/kagent#3263; the chart knobs of this patch become that binary''s flags. Its authenticator verifies every bearer and takes a person''s groups from the verified token only, never from a request header; agents are authorized by the actor policy of kagent-dev/kagent#3282, never with a person''s groups. First step, target kagent-dev/kagent: a review backing #3299 (owned-resource RBAC, third party), which closes this row''s session Create gap in place of a pull request of ours. Until the binary: the controller refuses every call carrying X-Agent-Name in kubernetes mode (giantswarm/kagent-upstream#837), and the platform edge removes x-agent-name and x-user-groups. Test for the binary: this fork''s TestKubernetesAuthorization (go/core/test/e2e/authorization_test.go) run against the binary''s image, plus the platform lab''s tenants edge steps E1 to E7. Dependencies: kagent-dev/kagent#3299, theme T12 (tenant = namespace), theme T3 (same repository). Exit: the binary is on the line and #3299 is merged; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Machine callers: OIDC discovery of the clusters ServiceAccount issuer, with pod-binding claims required, a lifetime cap, an allowlist of callers and synthesised ServiceAccount groups (decided 2026-10-22). Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: [kagent-dev/kagent#3299]
verified: 2026-10-22
---
# feat(auth): authorize each caller against Kubernetes RBAC with SubjectAccessReviews

## Change

`feat(auth): authorize each caller against Kubernetes RBAC with SubjectAccessReviews`

## Reason

on the API v2 line the controller authenticates the caller but authorizes nothing: every service runs `NoopAuthorizer`, so `ListAgents` returns every agent in a namespace to every caller and `CreateSession` starts a conversation on any Agent, while the Dev Portal reads the same objects through the Kubernetes proxy with the person's own token and RBAC applies there — a person RBAC denies `list agenttemplates` got 403 in the portal and the full roster through the edge (seen in the migration rehearsal on a test installation). Agents are tenant-scoped within an installation and Kubernetes RBAC on the kagent objects is the single source of truth, so the controller API has to give the answer the API server gives. The commit adds `KubernetesAuthorizer` on the existing `auth.CollectionAuthorizer` extension point, opt-in by `controller.authorization.mode: kubernetes` (default `noop`): every check is a `SubjectAccessReview` for the caller as the API server's OIDC authenticator maps the same token (the authenticated user ID with a prefix, the groups claim with a prefix, a name in the reserved `system:` space refused), an agent's calls for a person carry that person's groups (`X-User-Groups`, sent to the runtime with the turn and back by the Go and Python runtimes; on a turn a share admits, the owner's groups as the owner's last turn recorded them on the Session), a collection read is scoped to the namespaces in which the caller may `list` the resource (an empty roster when none; the namespaces a selector selects plus a named list, so the platform tenant ranges beside the organizations), the tool server and tool lists are scoped the same way, a new Session on `CreateSession` and on the first A2A `message/send` needs `get` on the Agent and `create` on `sessions.api.kagent.dev` in the Agent's namespace, every further turn needs `get` on the Agent for its sender, a checkpoint's fork needs the two reviews of a new Session, the owner-scoped records (a Session once it exists, checkpoints, scheduled runs, sandboxes) and the Substrate inventory are not reviewed, `all_creators` is refused for everyone, a check the API server does not answer is `Unavailable` rather than a refusal, decisions are cached ten seconds and concurrent misses share one review, bounded to five seconds and left by a caller at its own deadline; a token without the user ID claim is refused instead of read by its `sub`; the chart grants the controller `create` on `subjectaccessreviews` in that mode and renders an optional NetworkPolicy that keeps the controller's API port to the UI and the named callers, since the controller trusts the token and the user an agent's call names (giantswarm/kagent-upstream#571, which replaced #569, #568, #506 and #505: the reviews applied, the branch refreshed on the consumed head; the third review's findings applied in place on #571, the fourth review's as commits on it; squash-merged as one commit). The platform's connectivity chart turns the mode on

## History

to file — prepared in the fork: branch `upstream/kubernetes-authorizer-5` (`c1d7e201`, mirror `main` @ `92dd6caa`, the pin, + the patch and the fourth review's fixes but the owner's groups on a shared turn, which ride on the fork-only turn a share admits running as the owner, DCO-signed, no fork references, unit tests green there; its message is the pull-request text). kagent-dev/kagent searched 2026-10-13 ("SubjectAccessReview", "authorizer", "RBAC"): no authorizer implementation; the CEL authorization EP and the `allowed-groups` annotation pull request closed unmerged, the extension point (`app.Options.Authorizer`, EP-1270 scoped authorization) is what upstream chose

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 3 (out-of-tree binary on app.Options) plus a class 5 PR for the session Create check; upstream will not take an authorization engine in tree (kagent-dev/kagent#1270, EP-1270 non-goals, #1766 and #2075 stale).

2026-10-22: giantswarm/kagent-upstream#837 landed as one squash commit, `fix(auth): refuse unverified identities on the controller`, which carries its four commits (the ServiceAccount check, the agent-call refusal, the groups round-trip removal and the person's token verification); the row names that commit.

2026-10-22: amended in the theme decision (amendment 8, internal): machine callers by OIDC discovery of the ServiceAccount issuer; until the binary, agent calls are refused outright (giantswarm/kagent-upstream#837).
