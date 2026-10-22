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
  exit: 'class 5 upstream PR, the agent through kagent-dev/kagent#2901 (third party), the user through a PR of ours (U6). Mechanism for the agent: #2901''s opt-in ModelConfig.spec.sendAgentIDHeader sends X-Agent-Id: <namespace>/<name>, compiled per Agent, on the kagent runtime''s model calls (the Claude harness half is promised on the PR); the platform''s ModelConfigs set it and the gateway reads X-Agent-Id in place of x-kagent-agent and x-kagent-agent-namespace. Mechanism for the user: U6, the controller activates the usage/v1 A2A extension (go/api/a2a/usage.go on kagent-dev/kagent main b672187fd; emitted by the Go ADK, the harness runtime and the Python runtime) on every turn it forwards and records the turn''s tokens against the verified sender, as an OTel counter with an opt-in person attribute and one log event per turn; the person is never put on the model call. Chargeback joins it with the tokens the gateway measures per verified session (see runtime-name-the-session-on-every-model-call). First step, target kagent-dev/kagent: a review of #2901 with a lab run of its agentgateway header rule, and the Codex half offered on the author''s branch (see harness-name-the-agent-on-every-codex-model-call); then PR U6 (new branch). Tests: X-Agent-Id on the mock model server''s request with the option on and absent without it; the counter and the event carry the turn''s sender on a shared turn and no person without the opt-in. Dependencies: none. Exit: #2901 and U6 merged, the dashboards read X-Agent-Id and the U6 counter; the patch drops at the following re-pin, not before both are live. Re-check: 2026-11-17. Owner: unassigned. X-Agent-Id is attribution set by the runtime: rate limits keyed on it are not enforcement. Drafts: comments of 2026-10-20 and 2026-10-22 on the theme issue (internal)'
depends_on: []
verified: 2026-10-22
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
