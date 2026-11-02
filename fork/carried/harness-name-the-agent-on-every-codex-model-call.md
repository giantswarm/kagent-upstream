---
id: harness-name-the-agent-on-every-codex-model-call
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(harness): name the agent on every codex model call'
upstream:
  outcome: superseded
  refs: [kagent-dev/kagent#2901, kagent-dev/kagent#3359, kagent-dev/kagent#3362]
  exit: 'class 3 platform on upstream mechanisms. kagent-dev/kagent#2901 is superseded: the maintainer asked for a generic shape instead of one header per attribute, and upstream merged kagent-dev/kagent#3359 (ModelConfig.spec.actorJwt, 2026-10-27; the Substrate gateway mints, injects and refreshes the Actor JWT in the provider''s credential header, end-to-end tested on the Codex harness too) and kagent-dev/kagent#3362 (AgentIdentityService.ResolveAgentIdentity, 2026-10-28; a service holding the Actor JWT with the kagent-agent-info audience gets the Agent''s namespace, name, UID, labels and annotations). The Codex half is no longer offered. Exit: the platform''s ModelConfigs set actorJwt, the LLM listener verifies the token and resolves the Agent through the lookup in place of x-kagent-agent and x-kagent-agent-namespace; the patch drops at the re-pin past a77e158b5. actorJwt replaces the provider API key and Bedrock rejects it, so Codex on Bedrock reaches Bedrock through the LLM listener, not directly. Re-check: 2026-11-17. Owner: unassigned'
depends_on: []
verified: 2026-11-02
---
# feat(harness): name the agent on every codex model call

## Change

`feat(harness): name the agent on every codex model call`

## Reason

a codex agent's model calls carried no agent identity, so the platform's `llm` listener, which labels usage and keys rate limits by `x-kagent-agent` and `x-kagent-agent-namespace`, read every codex call as an unknown agent (giantswarm/kagent-upstream#613). The codex compiler injects and owns `KAGENT_AGENT_TEMPLATE` like the kagent compiler, and the codex adapter names the agent on the OpenAI provider through `env_http_headers`, on Amazon Bedrock through literal `http_headers` (the pinned Codex 0.148.0 refuses `env_http_headers` on its built-in Bedrock provider)

## History

to file — prepared in the fork: branch `upstream/codex-model-call-identity-headers`, on top of `upstream/model-call-identity-headers` (the identity it extends, `runtime-name-the-agent-and-the-user.md`), so it is filed with or after that one

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 4: upstream issue on baggage coverage for the harnesses, Python and BYO runtimes, filed after the ADK member PR.

2026-11-02: kagent-dev/kagent#2901 superseded by kagent-dev/kagent#3359 (Actor JWT on model calls) and kagent-dev/kagent#3362 (Agent lookup from the Actor JWT), both merged; the Codex half on origin/upstream/codex-agent-id-header (17d27690f) is dropped.
