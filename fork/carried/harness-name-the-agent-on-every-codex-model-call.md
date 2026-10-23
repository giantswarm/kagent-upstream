---
id: harness-name-the-agent-on-every-codex-model-call
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(harness): name the agent on every codex model call'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#2901]
  exit: 'class 5 upstream PR, on kagent-dev/kagent#2901 (third party). Mechanism: #2901''s opt-in ModelConfig.spec.sendAgentIDHeader (X-Agent-Id: <namespace>/<name>); this patch is its Codex half. First step, target kagent-dev/kagent: offer the Codex half on #2901 as a comment or a commit on the author''s branch, reshaped from origin/upstream/codex-model-call-identity-headers (dd4b29c60) onto the same option (Codex through env_http_headers on its model provider), not a pull request of our own. Offered 2026-10-22 on #2901 from origin/upstream/codex-agent-id-header (17d27690f on #2901''s head 0846c9706). Test: a harness e2e that asserts X-Agent-Id on the mock model server''s request with the option on. Dependencies: kagent-dev/kagent#2901. Exit: the Codex half merged with or after #2901; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. The session member on the harnesses, the Python runtime and BYO images is the runtime baggage coverage issue of runtime-name-the-session-on-every-model-call. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: [kagent-dev/kagent#2901]
verified: 2026-10-23
---
# feat(harness): name the agent on every codex model call

## Change

`feat(harness): name the agent on every codex model call`

## Reason

a codex agent's model calls carried no agent identity, so the platform's `llm` listener, which labels usage and keys rate limits by `x-kagent-agent` and `x-kagent-agent-namespace`, read every codex call as an unknown agent (giantswarm/kagent-upstream#613). The codex compiler injects and owns `KAGENT_AGENT_TEMPLATE` like the kagent compiler, and the codex adapter names the agent on the OpenAI provider through `env_http_headers`, on Amazon Bedrock through literal `http_headers` (the pinned Codex 0.148.0 refuses `env_http_headers` on its built-in Bedrock provider)

## History

to file — prepared in the fork: branch `upstream/codex-model-call-identity-headers`, on top of `upstream/model-call-identity-headers` (the identity it extends, `runtime-name-the-agent-and-the-user.md`), so it is filed with or after that one

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 4: upstream issue on baggage coverage for the harnesses, Python and BYO runtimes, filed after the ADK member PR.
