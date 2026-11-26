---
id: session-check-a-shares-expiry-against-the-clock
fork: kagent
theme: identity-authorizer
category: bugfix
status: carried
commits:
  - 'fix(session): check a share''s expiry against the clock that set it'
upstream:
  outcome: to-file
  refs: [giantswarm/kagent-upstream#124, kagent-dev/kagent#2987]
  exit: 'class 5 upstream PR. Upstream sets expires_at from the controller''s clock (go/core/internal/service/session/service.go:345 on main 96c40300) and checks it against the database''s now() (go/core/internal/database/shares.go:77). First step, target kagent-dev/kagent: PR C5, new branch on main from this fork''s d8d582ad7 (+27/-18 in 7 files), acceptance uncertain (the maintainer questioned a skew guard on #2987). Test: a store test with a skewed database clock. Dependencies: none. Exit: merged, or dropped at the next re-pin if declined. Re-check: 2026-11-17. Owner: unassigned. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: []
verified: 2026-10-20
---
# fix(session): check a share's expiry against the clock that set it

## Change

`fix(session): check a share's expiry against the clock that set it`

## Reason

the controller sets a share's `expires_at` from its own clock and the token lookup compared it with Postgres `now()`, so skew between the two could move a share's expiry; the lookup compares with the controller's time

## History

to file against kagent-dev/kagent#2987 (`4dce53de`), the review fix of #124's Session port

kagent-dev/kagent#2987 merged 2026-10-02 (`4dce53de`, in the pin) is the Session port this fix is filed against, so it stays in `refs` as related work, giantswarm/kagent-upstream#124 as the change's own history, and the outcome stays to-file.

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 5: small PR, dropped if declined.
