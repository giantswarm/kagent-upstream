---
id: runtime-name-the-session-on-every-model-call
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(runtime): name the session on every model call'
upstream:
  outcome: none
  refs: [kagent-dev/kagent#2884]
  exit: 'class 2 upstream-has-it. Mechanism: the gen_ai.conversation.id baggage member the Go ADK already sets (go/adk/pkg/a2a/executor.go:287 on kagent-dev/kagent main 96c40300) with OTEL_PROPAGATORS including baggage; the gateway reads it in place of this header. First step: none upstream; the platform''s gateway configuration switches to baggage with the row runtime-name-the-agent-and-the-user. Test: the gateway''s usage label test reads the member. Dependencies: runtime-name-the-agent-and-the-user. Exit: the gateway reads baggage; the patch drops at the following re-pin. Re-check: when U2 of that row merges. Owner: unassigned'
depends_on: [runtime-name-the-agent-and-the-user]
verified: 2026-10-20
---
# feat(runtime): name the session on every model call

## Change

`feat(runtime): name the session on every model call`

## Reason

the model calls name the agent and the person of the turn (the row "name the agent and the user" above) but not the conversation, so the platform's `llm` listener cannot price one session (a Dev Portal conversation, a Slack thread) and a tenant's report cannot list its most expensive sessions. The a2a gateway's runtime dialer forwards the id of the `Session` it routed the turn to (the header name is unchanged) as `x-kagent-agent-instance-id`, the name of the header that routes a call to an instance, never a value the caller sent; the Go ADK's HTTP transport sets it on every model call of that turn and removes it from any call outside a turn, so no ModelConfig can name a session. An accounting identity, never an authorization input; only the model and embedding clients use that transport, so a sub-agent's A2A call is never routed by it. The Claude harness sends no per-turn headers yet

## History

to file — prepared in the fork, after the row above it, whose upstream filing it extends

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 2: upstream's conversation baggage member covers it; no filing.
