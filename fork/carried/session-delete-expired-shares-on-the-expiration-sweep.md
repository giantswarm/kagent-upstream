---
id: session-delete-expired-shares-on-the-expiration-sweep
fork: kagent
theme: identity-authorizer
category: bugfix
status: carried
commits:
  - 'fix(session): delete expired shares on the expiration sweep'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#2987]
  exit: 'class 5 upstream PR. Upstream''s ExpirationWorker deletes idle sessions only (go/core/internal/service/session/expiration.go:37-56 on main 96c40300); expired shares go by cascade or revoke. First step, target kagent-dev/kagent: PR C3 from origin/upstream/session-share-expiry-sweep (fdbb706ec, 108 commits behind main), +235/-52 in 7 files, acceptance likely (#2987 precedent). Test: the branch''s ExpirationWorker unit test. Dependencies: none. Exit: merged; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: []
verified: 2026-10-20
---
# fix(session): delete expired shares on the expiration sweep

## Change

`fix(session): delete expired shares on the expiration sweep`

## Reason

an expired share stops granting access at its `expires_at`, but its row stayed in `session_share` until the owner revoked it or the session was deleted; the platform's Slack gateway (giantswarm/klaus-gateway) keeps one share per thread, so every thread nobody writes to again left a dead row behind. The leader's expiration sweep deletes the shares expired at the controller's clock on its poll interval and counts them in `kagent.session.share_expired`; a zero idle TTL turns idle deletion off and no longer idles the worker

## History

to file against kagent-dev/kagent#2987 (`4dce53de`) — prepared in the fork: branch `upstream/session-share-expiry-sweep` (mirror `main` + the patch, DCO-signed, no fork references)

kagent-dev/kagent#2987 merged 2026-10-02 (`4dce53de`, in the pin) is the share expiry this sweep is filed against, so it stays in `refs` as related work and the outcome stays to-file.

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 5: filed from the prepared branch.
