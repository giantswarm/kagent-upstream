---
id: adk-send-the-placeholder-to-actor-jwt-hosts
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(adk): send the placeholder, not the caller''s bearer, to actor-JWT hosts'
upstream:
  outcome: drops-at-repin
  refs: [kagent-dev/kagent#3257, kagent-dev/kagent#3263]
  exit: "drops at the re-pin onto acc75416b (kagent-dev/kagent#3263) once the Generic agent chart renders tokenPassthrough on muster's RemoteMCPServer and the platform's UpstreamAuth sends the placeholder; re-check each re-pin"
depends_on: [harness-send-the-placeholder-to-actor-jwt-hosts, kagent-dev/kagent#3263]
verified: 2026-10-22
---
# feat(adk): send the placeholder, not the caller's bearer, to actor-JWT hosts

## Change

`feat(adk): send the placeholder, not the caller's bearer, to actor-JWT hosts`

## Reason

the claude, codex and omp harnesses send a server on one of the turn's actor-JWT hosts (`x-kagent-actor-jwt-hosts`) only the placeholder `Authorization`, which the egress gateway replaces with the Actor's own JWT; the Go ADK ignored the header and put the caller's bearer (`KAGENT_PROPAGATE_TOKEN`, an allowed `Authorization`) or the STS-exchanged token on every MCP request, muster's included. Its MCP round-tripper now sets `Authorization: Bearer kagent-credential-injected` on a request to an actor-JWT host whose static headers set no `Authorization`, whatever header source is configured, and every HTTP and SSE server gets the round-tripper; servers on other hosts and turns without actor-JWT hosts are unchanged. `TestActorJWTHostsTakeThePlaceholder`, `TestNoActorJWTHostsKeepsTheCallerCredential`, `TestCreateTransportSendsThePlaceholderWithoutHeaderConfig`

## History

to file with kagent-dev/kagent#3018, whose actor-JWT hosts it reads

2026-10-22, decided on internal issue 38652, comment 6340015449: class 2 upstream has it, with a class 3 platform step; upstream's Go ADK forwards the caller token of a tokenPassthrough server (kagent-dev/kagent#3257), and with an embedder whose `Authenticator.UpstreamAuth` swaps that token for `Bearer kagent-credential-injected` (kagent-dev/kagent#3263) the placeholder reaches muster's host with no fork code; destination the agent-manager Generic agent chart (tokenPassthrough on muster's server instead of `KAGENT_PROPAGATE_TOKEN`) and the platform binary's UpstreamAuth (theme T9); first step those two platform changes; branch none upstream; test `TestActorJWTHostsTakeThePlaceholder` until the re-pin, then the lab's caller-credential proof; depends on themes T9 and T13; owner Quentin.
