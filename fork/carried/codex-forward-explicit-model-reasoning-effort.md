---
id: codex-forward-explicit-model-reasoning-effort
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(codex): forward explicit model reasoning effort'
  - 'test(codex): reject unrelated options alongside effort'
upstream:
  outcome: to-file
  refs: []
  exit: 'Drop when upstream forwards ModelConfig reasoningEffort through the Codex compiler and runtime; re-check at each re-pin.'
depends_on: []
verified: 2026-11-11
---
# Codex forwards explicit model reasoning effort

## Change

`fix(codex): forward explicit model reasoning effort`

## Reason

A generic upstream gap: the OpenAI ModelConfig accepts reasoningEffort, but
Codex rejects that provider option and cannot forward it to its native runtime.
Carry the field through the compiler-owned JSON into model_reasoning_effort
and each turn/start effort, including resumed threads. Omission preserves the
native default. The additive optional field retains existing config versions
and conversation state. No new Kubernetes API is introduced.

## Upstream

to file — prepared in the fork: branch `upstream/codex-reasoning-effort`
(`42dea99fc`, DCO-signed, on upstream pin `92dd6caad`). The pinned upstream
compiler has the same rejection. Its tests cover API-key authentication;
the carried version additionally covers the fork's ChatGPT OAuth provider.
The current mirrored main compiler also rejects reasoningEffort. No upstream
issue or pull request was opened. CI passed on `ff652f9b8`
([CI workflow](https://github.com/giantswarm/kagent-upstream/actions/runs/43005611224));
subsequent review fixes require CI on their own head. Positive agentlab proof
of the fixed controller/harness, outgoing effort and an existing conversation
resumed after upgrade remains required before merge.

## History

- Prepared the compiler and runtime forwarding in `ff4cc4e56`, with regression
  coverage for explicit and omitted effort on new and resumed turns.
- Recorded the upstream-ready variant in `42dea99fc` on the pinned upstream
  base. Successful pre-merge agentlab proof remains required.
