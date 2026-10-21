---
id: harness-end-a-stalled-claude-turn
fork: kagent
theme: session-lifecycle
category: bugfix
status: carried
commits:
  - 'fix(harness): end a stalled Claude turn instead of leaving it working'
upstream:
  outcome: to-file
  refs: []
  exit: file the branch
depends_on: []
verified: 2026-10-21
---
# fix(harness): end a stalled Claude turn instead of leaving it working

## Change

`fix(harness): end a stalled Claude turn instead of leaving it working`

## Reason

Claude Code moves a Bash command that outlives its timeout and survives the interrupt to the background, reports its result, and then stays alive until that command ends, to start a turn of its own on its notification. The Claude driver waited for the process to exit after the result, so a command that never ended (a shell stalled before its first network request in the e2e suite) left the turn working until the caller's deadline, with no event and no failure; a Claude that stalled before reporting its session did the same. The driver now reads the `background_tasks_changed` list Claude reports: a result reported while that list names a task ends the turn at once, and any other result ends it once Claude has not exited within 10 seconds; either way Claude's process group is stopped, the background command with it, the person is told what was stopped, and the turn ends with the result Claude reported. A Claude that reports no session within two minutes of its start is stopped and its turn fails with the stall and Claude's stderr.

## History

to file: upstream's Claude driver waits for the exit after the result the same way; the branch is prepared in the fork as `upstream/claude-turn-ends-when-claude-holds-on`.
