---
id: harness-claude-reaches-actor-jwt-hosts-without-the
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(harness): Claude reaches actor-JWT hosts without the forwarder'
  - 'refactor(harness): one header rule for MCP servers on actor-JWT hosts'
  - 'fix(harness): Claude sends the placeholder to actor-JWT hosts on every server'
  - 'fix(harness): the forwarder learns the turn''s actor-JWT hosts without the caller''s credential'
  - 'test(harness): the root adapter test reads the MCP configuration a turn writes'
  - 'fix(harness): no loopback token for a tool selection on an actor-JWT host'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3258, kagent-dev/kagent#3263, kagent-dev/kagent#3257]
  exit: "a kagent-dev/kagent issue on gateway token passthrough for the Claude, Codex and BYO harnesses (kagent-dev/kagent#3263 lists them as follow-ups) answered and its change merged: drops at the re-pin containing it; re-check each re-pin"
depends_on: [harness-send-the-placeholder-to-actor-jwt-hosts, harness-codex-and-omp-send-the-placeholder-to-actor]
verified: 2026-10-22
---
# feat(harness): Claude reaches actor-JWT hosts without the forwarder

## Change

`feat(harness): Claude reaches actor-JWT hosts without the forwarder`, `refactor(harness): one header rule for MCP servers on actor-JWT hosts`, `fix(harness): Claude sends the placeholder to actor-JWT hosts on every server`, `fix(harness): the forwarder learns the turn's actor-JWT hosts without the caller's credential` `test(harness): the root adapter test reads the MCP configuration a turn writes` and `fix(harness): no loopback token for a tool selection on an actor-JWT host`

## Reason

with the placeholder sent to actor-JWT hosts, the forwarder adds nothing to a call to muster that the egress gateway does not replace, yet `mcp.json`, readable by Claude's user and so by every process of the sandbox, still carried the forwarder's loopback token, which any of them could use during the turn. The harness now rewrites `mcp.json` before each turn's Claude process starts, and before anything runs as Claude's user, and writes a server on one of the turn's actor-JWT hosts without a tool selection with its compiled URL and headers and the placeholder bearer, with or without the caller's credential, so a Session whose every MCP server is on one holds no loopback token in the sandbox. A server with a tool selection stays on the forwarder, which learns the turn's hosts without the caller's credential too; its entry carries the placeholder, which the forwarder takes in place of its loopback token for a server on one of the turn's hosts, so an agent that selects tools of muster holds no loopback token either. The claude, codex and omp harnesses build a direct server's headers with one rule, `workspace.ActorJWTHeaders`. `TestMCPConfigLeavesTheLoopbackTokenOutOfAnActorJWTOnlyTurn`, `TestMCPConfigSendsThePlaceholderWithoutTheForwarder`, `TestMCPConfigKeepsASelectionOnTheForwarder`, `TestProcessDriverWritesTheMCPConfigBeforeTheSessionDirectory`, `TestProcessDriverBindsTheCallerCredentialPerTurn`, `TestActorJWTHostsOnlyBindsNoCallerCredential`, `TestCredentialForwarderAdmitsThePlaceholderForActorJWTHosts`, `TestActorJWTHeaders`

## History

exits with kagent-dev/kagent#3263, which builds on #3257's `RemoteMCPServer.spec.tokenPassthrough`: the egress gateway delivers the credential and the Claude translator compiles the placeholder into `mcp.json`, so neither the forwarder nor the per-turn rewrite is needed for muster: the platform's `ate-secret` provider would pick the Actor's JWT or the caller's bearer per request, provided the gateway tells it which Actor a request comes from. A server with a tool selection still needs the forwarder, since upstream's Claude translator only warns about a selection it cannot enforce. Supersedes the exit through kagent-dev/kagent#3018

2026-10-20, decided on internal issue 38652, comment 6301541135: class 5 upstream PR; destination kagent-dev/kagent, the Claude and Codex translators compile a tokenPassthrough RemoteMCPServer with the placeholder bearer instead of refusing it (a gap #3258 lists); first step that PR, uncapped; branch new (`upstream/translators-token-passthrough`); test `TestMCPConfigSendsThePlaceholderWithoutTheForwarder`; depends on no theme; owner unassigned; draft: internal issue 38652, comment 6301541554.

2026-10-22, updated on internal issue 38652: the destination is no longer a translators pull request of our own but one kagent-dev/kagent issue covering Claude, Codex and BYO: upstream refuses tokenPassthrough for MCP servers and models on Claude and Codex, the BYO compiler records passthrough targets with no contract for the runtime, and kagent-dev/kagent#3018 (a Claude caller-credential forwarder) serves the runtime-forwarding mode, not gateway passthrough; first step that issue, opened by a person; owner Quentin.
