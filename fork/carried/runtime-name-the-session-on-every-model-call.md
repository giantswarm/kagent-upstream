---
id: runtime-name-the-session-on-every-model-call
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(runtime): name the session on every model call'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#2884, kagent-dev/kagent#3263]
  exit: 'class 3 platform + 4 upstream issue. Mechanism: the session from the actor JWT. The Substrate egress already injects an actor JWT into a header the runtime sends (an actor_jwt credential header); an egress rule through the EgressPolicy compiler seam of kagent-dev/kagent#3263 puts the actor JWT, audience the platform''s LLM listener, in Authorization on model calls, and the listener verifies it with agentgateway jwtAuthentication, strips it and attaches the provider credential itself. The verified subject names the atespace and the actor (session-<Session id>), so tenant and session are verified inputs to rate limits and usage. ModelConfig types whose Authorization carries the person''s own login (chatgptOAuth) cannot be swapped; for them the gen_ai.conversation.id baggage member (go/adk/pkg/a2a/executor.go:287 on kagent-dev/kagent main 96c40300) stays the source, and the harnesses need the runtime baggage coverage issue (Claude through ANTHROPIC_CUSTOM_HEADERS, Codex through env_http_headers). First step, target the platform: list per ModelConfig type what Authorization carries toward the listener. Upstream issue, target Substrate and agentgateway: the egress forwards the verified actor identity to the next hop. Test: the platform lab asserts the listener refuses a model call without a valid actor JWT from the egress and that the provider request carries only the provider credential. Dependencies: theme T3 (the compiler seam). Exit: every ModelConfig type the platform renders is covered by the swap or by baggage, and the gateway reads them; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Draft: comments of 2026-10-20 and 2026-10-22 on the theme issue (internal)'
depends_on: []
verified: 2026-10-22
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
