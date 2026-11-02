---
id: runtime-name-the-session-on-every-model-call
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(runtime): name the session on every model call'
upstream:
  outcome: superseded
  refs: [kagent-dev/kagent#2884, kagent-dev/kagent#3263, kagent-dev/kagent#2901, agent-substrate/substrate#3137, kagent-dev/kagent#3359, kagent-dev/kagent#3362]
  exit: 'class 3 platform on an upstream mechanism, class 5 upstream PR for the remainder. Verified value: kagent-dev/kagent#3359 (ModelConfig.spec.actorJwt, merged 2026-10-27) has the Substrate gateway put the Actor JWT in the provider''s credential header of every managed model call, through the SDK placeholder, so neither the EgressPolicy compiler seam (kagent-dev/kagent#3263) nor agent-substrate/substrate#3137 is needed for managed clients; the token names the session-named actor and kagent-dev/kagent#3362 (merged 2026-10-28) returns its Session ID with the Agent. The LLM listener verifies it with jwtAuthentication. Remainder: ModelConfig types whose credential is the person''s own login (chatgptOAuth) or that reject actorJwt (Bedrock, Gemini, Ollama, Vertex) keep the conversation baggage member as the source, so the baggage coverage PR for the harnesses, the Python runtime and BYO images (drafted 2026-10-20, redraft for the session member only) still applies to them; agent-substrate/substrate#3137 stays open for clients that send no placeholder. Test: the listener''s session label equals the Session ID the lookup returns. Dependencies: none. Exit: the platform''s ModelConfigs set actorJwt and the listener reads the session from the token; the patch drops at the re-pin past a77e158b5. Re-check: 2026-11-17. Owner: unassigned. Drafts: comments of 2026-10-20 and 2026-10-22 on the theme issue (internal)'
depends_on: []
verified: 2026-11-02
---
# feat(runtime): name the session on every model call

## Change

`feat(runtime): name the session on every model call`

## Reason

the model calls name the agent and the person of the turn (the row "name the agent and the user" above) but not the conversation, so the platform's `llm` listener cannot price one session (a Dev Portal conversation, a Slack thread) and a tenant's report cannot list its most expensive sessions. The a2a gateway's runtime dialer forwards the id of the `Session` it routed the turn to (the header name is unchanged) as `x-kagent-agent-instance-id`, the name of the header that routes a call to an instance, never a value the caller sent; the Go ADK's HTTP transport sets it on every model call of that turn and removes it from any call outside a turn, so no ModelConfig can name a session. An accounting identity, never an authorization input; only the model and embedding clients use that transport, so a sub-agent's A2A call is never routed by it. The Claude harness sends no per-turn headers yet

## History

to file — prepared in the fork, after the row above it, whose upstream filing it extends

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 2: upstream's conversation baggage member covers it; no filing.

2026-10-22: amended in the theme decision (amendment 8, internal): the session comes from the actor JWT verified by the LLM listener; baggage stays only for ModelConfig types whose Authorization cannot be swapped.

2026-11-02: the verified value is upstream: kagent-dev/kagent#3359 puts the Actor JWT on managed model calls without a placeholder change in Substrate, and kagent-dev/kagent#3362 resolves its Session; baggage stays for model types that cannot carry the token.
