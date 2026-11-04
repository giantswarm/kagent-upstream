---
id: session-delete-another-creators-session-when
fork: kagent
theme: identity-authorizer
category: bugfix
status: carried
commits:
  - 'fix(session): delete another creator''s Session when the authorizer admits it'
upstream:
  outcome: to-file
  refs: [giantswarm/kagent-extensions#27, giantswarm/kagent-extensions#28]
  exit: 'class 5 upstream PR (a small generic fix on the authorization interface upstream chose, app.Options.Authorizer). Prepared in the fork: branch upstream/session-delete-another-creators-session, on mirror main@12aa4d46 (not on the pin 92dd6caa: upstream main rewrote getAuthorized and the share checks after the pin, so a branch on the pin would not apply); a human opens the upstream pull request. Exit: merged upstream; the commit drops at the following re-pin. The fork-only half, the Kubernetes authorizer reviewing the delete as a cluster-wide delete, rides on auth-authorize-each-caller-against-kubernetes-rbac and leaves with it.'
depends_on: [auth-authorize-each-caller-against-kubernetes-rbac, giantswarm/kagent-extensions#28]
verified: 2026-11-04
---
# fix(session): delete another creator's Session when the authorizer admits it

## Change

`fix(session): delete another creator's Session when the authorizer admits it`

## Reason

the platform's organization erasure lists every creator's Sessions of a deleted organization's Agents with `ListSessions{all_creators: true}` and deletes each by ID through `DeleteSession`, as a machine caller that RBAC grants cluster-wide `list` and `delete` on `sessions.api.kagent.dev`. The list works; the delete answered NotFound for every Session another person created, because `getAuthorized` loads the Session with the caller's creator filter unless the caller is the control plane, and no authorizer check widens a delete to other creators. The erasure therefore stopped at its Session step on every organization that held a person's Session (giantswarm/kagent-extensions#27). When a caller's own Session is not found, `Delete` (and nothing else that reads a Session) now asks the authorizer for verb `delete` on `SessionAllCreators`, the resource type `ListSessions` asks for `all_creators`; the Session is read by ID without the creator filter first, so a Session that does not exist stays NotFound for every caller and a repeated delete still reads as done (the line's e2e cleanup deletes Sessions the idle expiration already deleted); for an existing Session, an admitted caller deletes it and a refused one gets `PermissionDenied` with the authorizer's reason. A caller that holds another creator's Session ID thus learns that the Session exists; the ID is a random UUID it was handed. A caller's own delete asks no further check, a share stays with its own Session, and the other operations keep the creator filter. The Kubernetes authorizer and Giant Swarm's kagent-extensions authorizer review that check as a cluster-wide `delete` on `sessions.api.kagent.dev` (`fix(auth): review a delete of another creator's Session as a cluster-wide delete` and giantswarm/kagent-extensions#27), so the `list` grant of `all_creators` does not admit it. `NoopAuthorizer` refuses that one check and permits every other: without an authorizer the creator filter is the only limit on whose Session a caller deletes, so the delete of another creator's Session needs an authorizer that grants it. The pinned kagent-extensions commit (`go/kagent-extensions.commit`) must carry giantswarm/kagent-extensions#28, which reviews the check as a cluster-wide `delete`: the commit pinned before reviewed it as a cluster-wide `list`. Docs: `docs/architecture/oidc-proxy-authentication.md`, the authorization table

## History

to file — prepared in the fork: branch `upstream/session-delete-another-creators-session` (mirror `main` @ `12aa4d46` + the patch, not the pin `92dd6caa`, because upstream rewrote `getAuthorized` and the share checks after the pin; DCO-signed, no fork references; its message is the pull-request text). kagent-dev/kagent searched 2026-11-04 ("delete session another user", "SessionAllCreators", "all_creators"): no issue or pull request on deleting another creator's Session; #3051 is the UI exposing other users' titles.
