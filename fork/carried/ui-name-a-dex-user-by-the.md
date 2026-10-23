---
id: ui-name-a-dex-user-by-the
fork: kagent
theme: identity-authorizer
category: bugfix
status: carried
commits:
  - 'fix(ui): name a Dex user by the email, not the encoded subject'
upstream:
  outcome: to-file
  refs: []
  exit: 'class 5 upstream PR. oauth2-proxy''s user ID claim defaults to email (pkg/apis/options/providers.go, EnsureDefaults), so a stock configuration already names the person by email; the fix still reads email before user in the auth source (ui/src/auth/oauth2ProxyAuthSource.ts:40-47 on main 96c40300) for deployments that set user-id-claim. First step, target kagent-dev/kagent: PR from origin/upstream/ui-dex-display-email (c82b0b97d, 66 commits behind main; +15/-2), acceptance uncertain. Test: the UI unit test of the auth source. Dependencies: none. Exit: merged, or dropped at the next re-pin if declined. Re-check: 2026-11-17. Owner: unassigned. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: []
verified: 2026-10-23
---
# fix(ui): name a Dex user by the email, not the encoded subject

## Change

`fix(ui): name a Dex user by the email, not the encoded subject`

## Reason

behind oauth2-proxy with a Dex issuer, the userinfo document has no username or name and its `user` is Dex's encoded subject, a base64 protobuf of the user id and connector id; `toAuthUser` read `user` before `email`, so the header and the sign-in card named the person by that opaque string. The display name now takes `email` before `user`, which stays the last fallback

## History

to file, `upstream/ui-dex-display-email`

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 2 on the oauth2-proxy default; the prepared PR is filed only if a live userinfo response contradicts it.
