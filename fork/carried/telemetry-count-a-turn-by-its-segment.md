---
id: telemetry-count-a-turn-by-its-segment
fork: kagent
theme: identity-authorizer
category: bugfix
status: carried
commits:
  - 'fix(telemetry): count a turn by its segment, not by its invoke_agent spans'
upstream:
  outcome: to-file
  refs: []
  exit: 'class 5 upstream PR. Upstream pins Claude Code 2.1.285 (go/harness/claude/Dockerfile:23 on main 96c40300) and its e2e counts invoke_agent spans (go/core/test/e2e/tracing_test.go:418-425), which breaks at its next Claude Code bump. First step, target kagent-dev/kagent: PR U1 from origin/upstream/telemetry-count-turns-by-segment (a33decbac, 66 commits behind main), +48/-27 in 5 files, acceptance likely, open when ready. Test: the e2e tracing test. Dependencies: none. Exit: merged; the patch drops at the following re-pin. Re-check: 2026-11-17. Owner: unassigned. Draft: comment of 2026-10-20 on the theme issue (internal)'
depends_on: []
verified: 2026-10-20
---
# fix(telemetry): count a turn by its segment, not by its invoke_agent spans

## Change

`fix(telemetry): count a turn by its segment, not by its invoke_agent spans`

## Reason

Claude Code 2.1.302 marks its `claude_code.interaction` span with `gen_ai.operation.name=invoke_agent` (2.1.285 emits the span without it), beneath the `invoke_agent` span kagent opens for the harness, so the tracing e2e saw two invoke_agent spans and the Claude Code bump could not land; the contract now states that a native agent may mark its own span `invoke_agent` under kagent's and that turns are counted by `kagent.invocation.segment`, and the test selects the invocation that way and requires Claude Code's span under it

## History

to file — prepared in the fork: branch `upstream/telemetry-count-turns-by-segment`

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 5: small, likely, open when ready.
