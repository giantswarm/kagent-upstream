---
id: cli-send-a-caller-token-as-the-bearer
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(cli): send a caller token as the bearer of every controller call'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3209]
  exit: 'class 5 upstream PR, low priority. Reshape on kagent-dev/kagent#3209''s WithGRPCDialOptions (go/core/cli/root.go:28-33 on main 96c40300): the kubeconfig credential or KAGENT_TOKEN as a per-RPC bearer dial option, from origin/upstream/cli-caller-token (14e283ba9, 121 commits behind main; +605/-44 today, estimate +120 after the reshape), acceptance uncertain. No consumer on the platform today. Test: a CLI unit test that the dial options carry the bearer. Dependencies: none. Exit: merged; or dropped at a re-pin if still unconsumed. Re-check: when a consumer appears or the queue ahead is empty. Owner: unassigned. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: []
verified: 2026-10-20
---
# feat(cli): send a caller token as the bearer of every controller call

## Change

`feat(cli): send a caller token as the bearer of every controller call`

## Reason

the CLI reached the controller with `x-user-id` alone, so against a controller in `trusted-proxy` mode, which identifies the caller by the bearer its proxy verified, `session create` and `session invoke` were refused with `Unauthenticated: invalid credentials` (giantswarm/kagent-upstream#180); the CLI now sends `authorization: Bearer …` on every control-plane gRPC call and on A2A calls, from `--caller-token`, `KAGENT_TOKEN` or the current kubeconfig context's credential (an exec plugin, an id-token or a static token, read through client-go as kubectl sends it), `invoke --token`'s model key keeps the A2A bearer when both are set, and a refusal names what to do. Docs: `docs/architecture/oidc-proxy-authentication.md` "CLI", `docs/env.md`

## History

to file — prepared in the fork: branch `upstream/cli-caller-token` (mirror `main` + the patch, DCO-signed, no fork references)

kagent-dev/kagent#3209 merged 2026-10-15 (`2ab16889`) covers part: an embedding binary adds a bearer through gRPC dial options and `agent invoke --token` is gone, so the outcome stays to-file for the stock CLI's own bearer.

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 5 at low priority: kept, reshaped on #3209's dial options; no consumer today.
