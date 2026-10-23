---
id: runtime-name-the-session-on-every-model-call
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(runtime): name the session on every model call'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#2884, kagent-dev/kagent#3263, kagent-dev/kagent#2901, agent-substrate/substrate#3137]
  exit: 'class 5 upstream PR for the label, class 3 platform + 4 upstream issue for the verified value. PR, target kagent-dev/kagent: baggage coverage for the harnesses, the Python runtime and BYO images for the conversation member (gen_ai.conversation.id is fixed for an actor''s life, so a boot-time baggage header: Claude Code through its custom-headers environment variable, Codex through env_http_headers on its model provider, the Python bootstrap through the WithBaggage equivalent; the agent comes from kagent-dev/kagent#2901), reshaped from this patch''s headers onto baggage, estimate +80, acceptance likely (docs telemetry.md:141-145 names the gap on main 96c40300); drafted 2026-10-20, to redraft for the session member only. Test: a harness e2e asserting the baggage header on the mock model server''s request. Verified value: the session from the actor JWT that the Substrate egress puts in Authorization toward the platform''s LLM listener (an egress rule through the EgressPolicy compiler seam of kagent-dev/kagent#3263), verified with agentgateway jwtAuthentication; upstream ask agent-substrate/substrate#3137 (filed 2026-10-22); ModelConfig types whose Authorization carries the person''s own login (chatgptOAuth) keep the baggage member as the source. Dependencies: theme T3 (the compiler seam). Exit: the PR merged and the gateway reads the baggage member; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Drafts: comments of 2026-10-20 and 2026-10-22 on the theme issue (internal)'
depends_on: []
verified: 2026-10-23
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
