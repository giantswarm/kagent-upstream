---
id: sessions-mount-the-tool-images-a
fork: kagent
theme: harness-variety
category: feature
status: carried
commits:
  - 'feat(sessions): mount the tool images a session is created with'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3192, agent-substrate/substrate#1051, agent-substrate/substrate#2754]
  exit: the upstream pull request from branch upstream/session-tool-images merged; the re-pin past it keeps only the line's repoint of a toolset session
depends_on: [harness-mount-tool-images-read-only-into]
verified: 2026-10-22
---
# feat(sessions): mount the tool images a session is created with

## Change

`feat(sessions): mount the tool images a session is created with`

## Reason

the platform's coding agents pick their toolchains per Session (Go 1.26 for one Session, Go 1.25 for another of the same Agent), and a Harness's tool images serve every Session of every Agent on it. `CreateSession` takes up to 16 `tool_images` (a name, a digest-pinned `oci` reference, the `egress_destinations` the tools need); the controller prepares a toolset revision from the Agent's latest successful revision, the same ActorTemplate with the image volumes, mounts and tool path added and the origins added to its egress, shared by every Session of that revision naming the same set, and records it with the Session. The node pulls a tool image outside the runtime's egress, so the controller admits only images from `KAGENT_SESSION_TOOL_IMAGE_REPOSITORIES` (Helm `controller.sessionToolImageRepositories`, empty by default: every session tool image refused), and a tool image may name only the egress origins in `KAGENT_SESSION_TOOL_IMAGE_EGRESS_ORIGINS` (Helm `controller.sessionToolImageEgressOrigins`, empty by default), so a caller cannot widen a runtime's egress beyond what the operator allows. Resume, forks and checkpoints keep a Session's tool images and their origins; a Session never reaches another toolset's origins.

Two parts are the line's, not upstream's. The line's ActorTemplates carry a default egress policy, which Substrate gives a new Actor at `CreateActor`: a toolset template compiles it from the toolset's access instead of copying the base template's, or the Session's own policy conflicts and the creation fails `UNAVAILABLE`. And the line moves a quiesced or resuming Session onto its Agent's current revision, which Substrate refuses for a template whose volumes differ, so a toolset Session never moved: it now moves onto the toolset revision derived from the current revision for its own tool images, the template created by the first repoint and taken once its golden snapshot is ready (the Session stays meanwhile, no turn waits for it), the revision recorded in the transaction of the move. Egress grants follow the Agent, so the toolset holds its base revision's. No persisted format changes beyond an added optional field (`Session.tool_images`, `Checkpoint.tool_images`), no migration: a toolset revision is an ordinary Agent runtime revision row. Docs: `docs/architecture/configuration-and-compilation.md`

## History

to file — prepared in the fork: branch `upstream/session-tool-images-4`, one commit on the head of kagent-dev/kagent#3192 (`8940241fb`) (`gh search prs -R kagent-dev/kagent "session tool images"`: none, 2026-10-22)
