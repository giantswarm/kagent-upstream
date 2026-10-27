---
id: harness-mark-the-workspace-setup-notice
fork: kagent
theme: workspace-volumes
category: feature
status: carried
commits:
  - 'feat(harness): mark the workspace setup notice in its status metadata'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3157, kagent-dev/kagent#3158]
  exit: 'folds into the session-directory change with #3157 and #3158, which carry the notice it marks'
depends_on: [adk-set-up-the-session-directory, harness-claude-in-its-session-directory-changed, harness-codex-works-in-its-session-directory]
verified: 2026-10-27
---
# feat(harness): mark the workspace setup notice in its status metadata

## Change

`feat(harness): mark the workspace setup notice in its status metadata`

## Reason

The notice a turn starts with after its workspace setup (a setup that failed, a returning session's directory set up again, repositories left at the workspace's last sync) is an agent-role `working` status message, the same shape as the agent's own text. A client could tell it apart only by its text and position, so a reply whose first words matched the notice's prefix showed as a notice, and a notice showed as the agent's answer to a client that did not match the text. The message now carries `kagent.dev/a2a/notice: workspace-setup` in its metadata (`apia2a.NoticeMetadataKey`, `apia2a.WorkspaceSetupNotice`), set by the Go ADK and, through `runtime.Notice.Kind`, by the claude and codex harnesses; a runtime notice without a kind carries no mark. The key sits under upstream's `kagent.dev/a2a/` metadata prefix beside `stop-reason` and `failure-reason`. Upstream has no workspace setup and no notice, so the mark goes upstream with the session-directory change that adds them.

## History

Proven by unit tests on the A2A runtime executor (a notice with and without a kind), the Go ADK executor (a failed fetch's notice marked, the answer not) and both harness drivers. Not yet on the `upstream/session-first-turn-fetch` branch.
