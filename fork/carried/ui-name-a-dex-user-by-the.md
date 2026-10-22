---
id: ui-name-a-dex-user-by-the
fork: kagent
theme: identity-authorizer
category: bugfix
status: carried
commits:
  - 'fix(ui): name a Dex user by the email, not the encoded subject'
upstream:
  outcome: none
  refs: []
  exit: 'class 2 upstream-has-it (configuration). oauth2-proxy''s user ID claim defaults to email (pkg/apis/options/providers.go, EnsureDefaults) and /oauth2/userinfo returns that value as user, so on a stock configuration the UI already names the person by email; neither the upstream chart nor the platform sets user-id-claim. Trigger: a live /oauth2/userinfo response whose user is the encoded subject; then file origin/upstream/ui-dex-display-email (c82b0b97d, 66 commits behind main; +15/-2) as a class 5 PR. Test: the UI unit test of the auth source. Dependencies: none. Exit: the patch drops at the next re-pin unless the trigger fires. Owner: unassigned'
depends_on: []
verified: 2026-10-20
---
# fix(ui): name a Dex user by the email, not the encoded subject

## Change

`fix(ui): name a Dex user by the email, not the encoded subject`

## Reason

behind oauth2-proxy with a Dex issuer, the userinfo document has no username or name and its `user` is Dex's encoded subject, a base64 protobuf of the user id and connector id; `toAuthUser` read `user` before `email`, so the header and the sign-in card named the person by that opaque string. The display name now takes `email` before `user`, which stays the last fallback

## History

to file, `upstream/ui-dex-display-email`

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 2 on the oauth2-proxy default; the prepared PR is filed only if a live userinfo response contradicts it.
