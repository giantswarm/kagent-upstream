---
id: harness-claude-code-sends-no-nonessential-traffic
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(harness): Claude Code sends no traffic past its model route but the model''s'
upstream:
  outcome: open
  refs: [giantswarm/kagent-upstream#812, kagent-dev/kagent#3439]
  exit: 'kagent-dev/kagent#3439 merged, then the re-pin drops the commit'
depends_on: []
verified: 2026-10-29
---
# fix(harness): Claude Code sends no traffic past its model route but the model's

## Change

`fix(harness): Claude Code sends no traffic past its model route but the model's`

## Reason

Claude Code reaches its model at `ANTHROPIC_BASE_URL`, but on every turn it also calls `api.anthropic.com` directly. Claude Code 2.1.303 makes five such requests: its bootstrap, a feature probe, the MCP registry, feature flags and first-party event logging. A sandbox whose egress admits only the model route refuses each one, so every turn raises egress refusals although the turn succeeds. The adapter now sets `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` next to `DISABLE_UPDATES`, and the name is Claude-owned, so a Harness cannot set it. Claude Code's native OTLP telemetry is unaffected. Upstream runs the same adapter, so the gap is generic. Prepared for upstream as branch `upstream/claude-nonessential-traffic`.

## History

to file — prepared in the fork: branch `upstream/claude-nonessential-traffic`

2026-10-29: filed as kagent-dev/kagent#3439 by fiunchinho from `upstream/claude-nonessential-traffic-2` (`dbe9a63ff`), the prepared commit re-cut on upstream `main` `a9a53207a` (+28/-8): it keeps the `CLAUDE_CODE_MAX_RETRIES` reservation upstream added beside the new name, sets both pinned names in a `pinnedEnvironment` helper with a unit test, and carries a one-line comment on the constant.
