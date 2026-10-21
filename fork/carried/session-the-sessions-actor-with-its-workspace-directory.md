---
id: session-the-sessions-actor-with-its-workspace-directory
fork: kagent
theme: workspace-volumes
category: feature
status: carried
commits:
  - 'feat(session): the Session''s actor with its workspace directory'
upstream:
  outcome: to-file
  refs: [agent-substrate/substrate#1637, agent-substrate/substrate#1988, agent-substrate/substrate#2567, agent-substrate/substrate#2828, agent-substrate/substrate#3053]
  exit: 'after as#3053 (on as#2828 and as#2567) and as#1988 merge upstream'
depends_on: [giantswarm/substrate#234]
verified: 2026-10-21
---
# feat(session): the Session's actor with its workspace directory

## Change

`feat(session): the Session's actor with its workspace directory`

## Reason

a Session admitted on a workspace volume works in its own directory of that one shared read-write-many volume: every revision's ActorTemplate declares two existing volumes (the Session's own directory read-write at `/workspace`, the shared mirrors read-only at `/mirrors`), and the Session's Actor supplies its volume source's directories for them at create (`${SESSION_ID}` expanded), through the Substrate line's mount of an existing volume at a sub-path (`Actor.existing_volumes`, Substrate 1.7.0); a source outside that layout or with a handle over Substrate's 256 characters is refused. An Actor without a source supplies none and its create request is byte-identical to before. Deleting the Session deletes its Actor and records a `SessionDirectoryReleased` Event naming the volume and the directory, for the volume's owner to delete; kagent never touches the volume. `Session.last_turn_time`, moved by every turn's dispatch and shown on get and list, drives the owner's cleanup of unused directories. Every dispatch carries the turn's working directory (`x-kagent-working-directory`: `/workspace` with a volume, `/data/workspace` without). The Kind lane moves from the CSI hostpath snapshot fixture to an NFS read-write-many class with the workspace's bare mirrors

## History

to file; with the volume source and the admission, once the Substrate mount of an existing volume (agent-substrate/substrate#1637) has landed upstream. It waits on agent-substrate/substrate#1637 and #1988.

The ref kagent-dev/kagent#1988 (an unrelated CEL validation pull request, merged 2026-06-12) is corrected to agent-substrate/substrate#1988 (open, configurable volume access modes), the item the exit names.

Upstream's mount of one shared volume across actors is a stack of open drafts under agent-substrate/substrate#1637: #2567 (an atespace-scoped `SharedVolume` an actor mounts, on the merged `Volume.external` union of #2653), #2828 (`VolumeMount.sub_path` and `VolumeMount.read_only`, the per-mount directory view and permission) and #3053 (`Actor.shared_volume_sub_paths`, set at `CreateActor` and keyed by the template's volume name, the per-actor directory). Together they are the upstream shape of the line's `Actor.existing_volumes` with `${SESSION_ID}` expanded: the filing reshapes this commit onto `external.shared_volume` with a per-actor sub-path once #3053 and its stack have merged, and concurrent read-write holders still need #1988's `READ_WRITE_MANY`. agent-substrate/substrate#1994 (the preview gate for external volumes) merged on 2026-10-15; the installation's ate-api enables it.
