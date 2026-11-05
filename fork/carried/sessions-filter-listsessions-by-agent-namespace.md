---
id: sessions-filter-listsessions-by-agent-namespace
fork: kagent
theme: session-lifecycle
category: feature
status: carried
commits:
  - 'feat(sessions): filter ListSessions by Agent namespace'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3532, kagent-dev/kagent#3531, giantswarm/agent-platform-operator#8]
  exit: 'kagent-dev/kagent#3531 merges and the line re-pins past it'
depends_on: []
verified: 2026-11-05
---
# feat(sessions): filter ListSessions by Agent namespace

## Change

`feat(sessions): filter ListSessions by Agent namespace`: `ListSessionsRequest.namespace` (field 7, optional, the namespace pattern) filters by the namespace of each session's pinned revision, in the store query and in the share path; combined with `agent`, both must match.

## Reason

The agent-platform-operator erases a deleted organization's sessions (giantswarm/agent-platform-operator#8). The `agent` filter needs each Agent's name, and an Agent deleted before the erasure began leaves no name in the cluster, while its sessions keep their pinned revision; without a namespace filter the operator lists every session of the installation with `all_creators` and keeps the namespace's. The carry ports kagent-dev/kagent#3531, written on upstream `main` (`kagent.api.v1`), to this line's `kagent.api.v1alpha1` on the same field number.

## History

2026-11-05: kagent-dev/kagent#3532 filed, kagent-dev/kagent#3531 opened.
