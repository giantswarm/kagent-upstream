---
id: harness-claude-code-sends-no-nonessential-traffic
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(harness): Claude Code sends no traffic past its model route but the model''s'
upstream:
  outcome: to-file
  refs: [giantswarm/kagent-upstream#812]
  exit: file
depends_on: []
verified: 2026-10-21
---
# fix(harness): Claude Code sends no traffic past its model route but the model's

## Change

`fix(harness): Claude Code sends no traffic past its model route but the model's`

## Reason

Claude Code reaches its model at `ANTHROPIC_BASE_URL`, but on every turn it also calls `api.anthropic.com` directly. Claude Code 2.1.303 makes five such requests: its bootstrap, a feature probe, the MCP registry, feature flags and first-party event logging. A sandbox whose egress admits only the model route refuses each one, so every turn raises egress refusals although the turn succeeds. The adapter now sets `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` next to `DISABLE_UPDATES`, and the name is Claude-owned, so a Harness cannot set it. Claude Code's native OTLP telemetry is unaffected. Upstream runs the same adapter, so the gap is generic. Prepared for upstream as branch `upstream/claude-nonessential-traffic`.

## History

to file — prepared in the fork: branch `upstream/claude-nonessential-traffic`
