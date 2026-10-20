---
id: harness-codex-and-omp-send-the-placeholder-to-actor
fork: kagent
theme: mcp-forwarder
category: feature
status: carried
commits:
  - 'feat(harness): codex and omp send the placeholder to actor-JWT hosts'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3258]
  exit: "the codex half with harness-claude-reaches-actor-jwt-hosts-without-the (the same kagent-dev/kagent issue); the omp half stays until the harness-variety theme moves omp out; re-check each re-pin"
depends_on: [harness-send-the-placeholder-to-actor-jwt-hosts]
verified: 2026-10-22
---
# feat(harness): codex and omp send the placeholder to actor-JWT hosts

## Change

`feat(harness): codex and omp send the placeholder to actor-JWT hosts`

## Reason

the egress gateway replaces only an `Authorization` header a request already carries, and on codex and omp a workspace Session's MCP calls to muster carried none, so muster answered 401 and served the person on claude only. The codex adapter now rewrites `config.toml` before each turn's App Server starts, and the omp adapter `mcp.json` before each omp process and its tool bridge per request, with `Authorization: Bearer kagent-credential-injected` for a server on one of the turn's actor-JWT hosts (`x-kagent-actor-jwt-hosts`) that sets no `Authorization` of its own. `TestRenderConfigSendsThePlaceholderToActorJWTHosts`, `TestProcessDriverParksAndResolvesMCPApproval`, `TestMCPFileSendsThePlaceholderToActorJWTHosts`, `TestBridgeTransportSendsThePlaceholderDuringAnActorJWTTurn`, `TestRunPreparesForTheTurnsActorJWTHosts`, `TestActorJWTHosts`, `TestTakesActorJWT`

## History

the codex half to file with `feat(harness): no caller credential to actor-JWT hosts`, after the actor-JWT providers it serves (`upstream/session-egress-destinations-2`); the omp harness is the line's own

2026-10-20, decided on internal issue 38652, comment 6301541135: class 5 upstream PR for the codex half (the same translators PR), class 3 platform for the omp half (theme T7); destination kagent-dev/kagent; first step the translators PR; branch new; test `TestRenderConfigSendsThePlaceholderToActorJWTHosts` and `TestMCPFileSendsThePlaceholderToActorJWTHosts`; depends on theme T7; owner unassigned; draft: internal issue 38652, comment 6301541554.

2026-10-22, updated on internal issue 38652: the codex half follows the Claude row's new destination, the kagent-dev/kagent issue on gateway token passthrough for the Claude, Codex and BYO harnesses; owner Quentin.

2026-10-22, moved to theme T10 (MCP forwarder, internal issue 38659): the harness passthrough issue is decided there, after the harness-variety theme settles whether codex runs as a BYO image.
