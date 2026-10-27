---
id: harness-mark-the-workspace-setup-notice
fork: kagent
theme: workspace-volumes
category: feature
status: carried
commits:
  - 'feat(harness): mark the workspace setup notice in its status metadata'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3157, kagent-dev/kagent#3158]
  exit: '#3157 and #3158 merged'
depends_on: [adk-set-up-the-session-directory, harness-claude-in-its-session-directory-changed, harness-codex-works-in-its-session-directory]
verified: 2026-10-27
---
# feat(harness): mark the workspace setup notice in its status metadata

## Change

`feat(harness): mark the workspace setup notice in its status metadata`

## Reason

The notice a turn starts with after its workspace setup (a setup that failed, a returning session's directory set up again, repositories left at the workspace's last sync) is an agent-role `working` status message, the same shape as the agent's own text. A client could tell it apart only by its text and position, so a reply whose first words matched the notice's prefix showed as a notice, and a notice showed as the agent's answer to a client that did not match the text. The message now carries `kagent.dev/a2a/notice: workspace-setup` in its metadata (`apia2a.NoticeMetadataKey`, `apia2a.WorkspaceSetupNotice`), set by the Go ADK and, through `runtime.Notice.Kind`, by the claude and codex harnesses; a runtime notice without a kind carries no mark. The key sits under upstream's `kagent.dev/a2a/` metadata prefix beside `stop-reason` and `failure-reason`. Upstream has no workspace setup and no notice, so the mark goes upstream with the session-directory change that adds them.

## History

Proven by unit tests on the A2A runtime executor (a notice with and without a kind), the Go ADK executor (a failed fetch's notice marked, the answer not) and both harness drivers.  On 2026-10-27 kagent-dev/kagent#3157 and #3158 were rebased onto upstream `main` @ `bc2a745ca` from `upstream/session-first-turn-fetch`, split back into one commit each (`cb75c2695` on `upstream/claude-session-directory`, `96990f006` on `upstream/adk-session-directory`), with the retry of a failed clone or fetch, the clone that keeps no mirror path as origin, the fetch failure label and the workspace setup notice mark folded in; the Go ADK part follows upstream's builtin tools (kagent-dev/kagent#3283), which have no scratch directory. kagent-dev/kagent#3155 and #3156 (the volume source on CreateSession) were closed on 2026-10-20 in favour of Substrate's shared volumes (agent-substrate/substrate#2567).
