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
  refs: []
  exit: 'class 4 upstream issue. Gap: the Claude Code and Codex harnesses, the Python runtime and BYO images do not add the GenAI identity to their model calls'' baggage (docs/architecture/telemetry.md:142 on kagent-dev/kagent main 96c40300; python/packages/kagent-core/tests/test_telemetry_bootstrap.py:175 propagates traceparent and tracestate only). First step, target kagent-dev/kagent: issue "runtime baggage coverage" with a boot-time static baggage header as the first step (the agent and the conversation are fixed for an actor''s life; Claude through ANTHROPIC_CUSTOM_HEADERS, Codex through env_http_headers), filed after PR U2 of runtime-name-the-agent-and-the-user; the PR is this patch reshaped onto baggage from origin/upstream/codex-model-call-identity-headers (dd4b29c60, 229 commits behind main). Test: a harness e2e that asserts the baggage header on the mock model server''s request. Dependencies: runtime-name-the-agent-and-the-user. Exit: the issue is accepted and its PR merged; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Attribution on this line while writable shares are carried: a static boot-time header names the session''s owner for a visitor''s turn; the person of the turn is on the controller''s invocation span only. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: [runtime-name-the-agent-and-the-user]
verified: 2026-10-20
---
# feat(harness): name the agent on every codex model call

## Change

`feat(harness): name the agent on every codex model call`

## Reason

a codex agent's model calls carried no agent identity, so the platform's `llm` listener, which labels usage and keys rate limits by `x-kagent-agent` and `x-kagent-agent-namespace`, read every codex call as an unknown agent (giantswarm/kagent-upstream#613). The codex compiler injects and owns `KAGENT_AGENT_TEMPLATE` like the kagent compiler, and the codex adapter names the agent on the OpenAI provider through `env_http_headers`, on Amazon Bedrock through literal `http_headers` (the pinned Codex 0.148.0 refuses `env_http_headers` on its built-in Bedrock provider)

## History

to file — prepared in the fork: branch `upstream/codex-model-call-identity-headers`, on top of `upstream/model-call-identity-headers` (the identity it extends, `runtime-name-the-agent-and-the-user.md`), so it is filed with or after that one

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 4: upstream issue on baggage coverage for the harnesses, Python and BYO runtimes, filed after the ADK member PR.
