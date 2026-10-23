---
id: harness-mount-tool-images-read-only-into
fork: kagent
theme: harness-variety
category: feature
status: carried
commits:
  - 'feat(harness): mount tool images read-only into the runtime'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3190, kagent-dev/kagent#3192]
  exit: 'with #3192'
depends_on: []
verified: 2026-10-22
---
# feat(harness): mount tool images read-only into the runtime

## Change

`feat(harness): mount tool images read-only into the runtime`

## Reason

a coding agent needs language toolchains (Go, Node, Helm, …) that the Harness image does not carry, and baking each one into the Harness image makes one image per combination. `Harness.spec.workload.toolImages` lists digest-pinned OCI images; the controller mounts each one read-only into the runtime as a Substrate image volume at `/opt/kagent/tools/<name>` and sets `KAGENT_TOOL_PATHS` to their `bin/` directories, which every managed runtime appends to its `PATH` after the image's own. The session tool images (`sessions-mount-the-tool-images-a.md`) build on this contract. Carried as the head of the upstream pull request

## History

open — kagent-dev/kagent#3192 (for kagent-dev/kagent#3190)
