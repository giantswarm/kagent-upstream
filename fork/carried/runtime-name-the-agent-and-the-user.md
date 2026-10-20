---
id: runtime-name-the-agent-and-the-user
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(runtime): name the agent and the user on every model call'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#2737, kagent-dev/kagent#2901, kagent-dev/kagent#2939, kagent-dev/kagent#2575, kagent-dev/kagent#2884]
  exit: 'class 2 upstream-has-it (the agent) + 5 upstream PR (the user). Mechanism: W3C baggage; the Go ADK adds gen_ai.agent.name, gen_ai.conversation.id and a2a.task.id to every model and MCP call''s baggage when the runtime''s OTEL_PROPAGATORS includes baggage (go/adk/pkg/a2a/executor.go:285-289, docs/architecture/telemetry.md:119-145 on kagent-dev/kagent main 96c40300); the gateway in front of the providers reads the baggage members instead of these headers and strips baggage toward the provider. First step, target kagent-dev/kagent: PR U2 "add the namespace-qualified gen_ai.agent.id to the request baggage" (new branch on main 96c40300, estimate +25, single concern); then PR C1 "enduser.id in the request baggage behind an opt-in" (new branch, estimate +120/-5; proposed runtime setting KAGENT_BAGGAGE_ENDUSER_ID=plain|hashed|off, default off, because the propagator alone would send the person''s identity to every provider and MCP server, the objection raised on #2901). Tests: the member is present; absent without the opt-in; hashed when asked. Dependencies: none. Exit: U2 and C1 merged and the gateway reads baggage; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Baggage is attribution only; rate limits and policies key on the authenticated workload identity. Drafts: comments of 2026-10-20 on the theme issue (internal)'
depends_on: []
verified: 2026-10-20
---
# feat(runtime): name the agent and the user on every model call

## Change

`feat(runtime): name the agent and the user on every model call`

## Reason

under the kagent API v2 every agent is a Substrate actor in a shared WorkerPool and every actor connection leaves the worker through the Substrate egress, so a gateway in front of the model providers (the platform's `llm` listener, which prices every call and labels usage per agent and user) sees one caller for every agent and no user: since the platform's cut-over every dollar of agent spend sat on the egress row and the `user` label was unknown. The controller now injects `KAGENT_AGENT_TEMPLATE` next to `KAGENT_NAMESPACE`; the Go ADK sets `x-kagent-agent` and `x-kagent-agent-namespace` on every model call after a ModelConfig's default headers (so none can name another agent) and `x-kagent-user`, the person of the turn — the identity the controller's trusted-proxy authenticator resolved from the caller's validated token, which the a2a gateway forwards to the actor as metadata of the same name; a session without claims (unsecure mode and its `admin@kagent.dev` default, an agent's propagated caller id) forwards nothing, so the header is absent, never a token or a stand-in; the Claude harness sends the two agent headers through `ANTHROPIC_CUSTOM_HEADERS`, which the controller renders and owns. The gateway reads the headers only from the egress's traffic (giantswarm/agent-platform#586)

## History

to file — prepared in the fork: branch `upstream/model-call-identity-headers`; kagent-dev/kagent#2901 (open: `X-Agent-Id` on the kagent runtime's model calls, issue #2737) overlaps the agent half, so the filing is coordinated there; at the re-pin the gateway hop moves to the runtime executor (#2939)

kagent-dev/kagent#2939 merged 2026-09-25 (`8be872b2`, in the pin) moved the gateway hop to the runtime executor, not this change's exit, so it stays in `refs` as related work and the outcome stays to-file.

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 2 for the agent (upstream's baggage), class 5 for the user; the x-kagent headers and #2901 are not pursued.
