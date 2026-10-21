---
id: adk-send-the-placeholder-to-actor-jwt-hosts
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(adk): send the placeholder, not the caller''s bearer, to actor-JWT hosts'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3018]
  exit: 'with #3018 and the harness placeholder it extends to the Go ADK'
depends_on: [harness-send-the-placeholder-to-actor-jwt-hosts]
verified: 2026-10-21
---
# feat(adk): send the placeholder, not the caller's bearer, to actor-JWT hosts

## Change

`feat(adk): send the placeholder, not the caller's bearer, to actor-JWT hosts`

## Reason

the claude, codex and omp harnesses send a server on one of the turn's actor-JWT hosts (`x-kagent-actor-jwt-hosts`) only the placeholder `Authorization`, which the egress gateway replaces with the Actor's own JWT; the Go ADK ignored the header and put the caller's bearer (`KAGENT_PROPAGATE_TOKEN`, an allowed `Authorization`) or the STS-exchanged token on every MCP request, muster's included. Its MCP round-tripper now sets `Authorization: Bearer kagent-credential-injected` on a request to an actor-JWT host whose static headers set no `Authorization`, whatever header source is configured, and every HTTP and SSE server gets the round-tripper; servers on other hosts and turns without actor-JWT hosts are unchanged. `TestActorJWTHostsTakeThePlaceholder`, `TestNoActorJWTHostsKeepsTheCallerCredential`, `TestCreateTransportSendsThePlaceholderWithoutHeaderConfig`

## History

to file with kagent-dev/kagent#3018, whose actor-JWT hosts it reads
