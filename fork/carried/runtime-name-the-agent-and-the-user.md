---
id: runtime-name-the-agent-and-the-user
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(runtime): name the agent and the user on every model call'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#2737, kagent-dev/kagent#2901, kagent-dev/kagent#2939, kagent-dev/kagent#2575, kagent-dev/kagent#2884, kagent-dev/kagent#3327, kagent-dev/kagent#3359, kagent-dev/kagent#3362]
  exit: 'the agent: class 3 platform on upstream mechanisms; the user: class 5 upstream PR of ours (U6). Agent: kagent-dev/kagent#2901 is superseded by kagent-dev/kagent#3359 (ModelConfig.spec.actorJwt, merged 2026-10-27: the Substrate gateway puts the Actor JWT in the provider''s credential header on every model call; tested on the kagent runtime, Claude, Codex and BYO on the Go ADK image) and kagent-dev/kagent#3362 (AgentIdentityService.ResolveAgentIdentity, merged 2026-10-28: the Actor JWT with the kagent-agent-info audience resolves to the Agent''s namespace, name, UID, labels and annotations). The platform''s ModelConfigs set actorJwt with the LLM listener''s audience and kagent-agent-info; the listener verifies the token with jwtAuthentication, resolves the Agent through the lookup (cached per token) in place of x-kagent-agent and x-kagent-agent-namespace, and holds the provider credential itself, since actorJwt replaces the API key. User: U6, kagent-dev/kagent#3327 (open): the controller records each turn''s tokens against its verified sender at settlement, as OTel counters without a person attribute and one log event per turn naming the sender; the person is never put on the model call. Chargeback joins it with the tokens the gateway measures per verified session (see runtime-name-the-session-on-every-model-call). Tests: the lookup returns the turn''s Agent for the token the listener received; the counters carry no person and the event names the turn''s sender on a shared turn. Dependencies: none. Exit: U6 merged, the listener reads the Agent from the lookup and the dashboards read it and the U6 counter; the patch drops at the following re-pin, not before both are live. Re-check: 2026-11-17. Owner: unassigned. Drafts: comments of 2026-10-20 and 2026-10-22 on the theme issue (internal)'
depends_on: []
verified: 2026-11-02
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

2026-10-22: amended in the theme decision (amendment 8, internal): the user half moves from C1 (enduser.id baggage) to U6 (controller usage accounting on usage/v1), so the person never rides the model call.

2026-11-02: the agent half moves off kagent-dev/kagent#2901, superseded by kagent-dev/kagent#3359 and kagent-dev/kagent#3362 (both merged): the LLM listener verifies the Actor JWT and looks the Agent up. U6 is open as kagent-dev/kagent#3327.
