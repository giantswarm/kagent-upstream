---
id: e2e-snapshotted-workspace-volume-and-grant-signer-fixtures
fork: kagent
theme: test-flakes
category: test
status: carried
commits:
  - 'chore(e2e): snapshotted workspace volume and grant signer fixtures'
upstream:
  outcome: to-file
  refs: []
  exit: 'with the Session volume source series'
depends_on: [session-the-sessions-actor-with-its-workspace-directory]
verified: 2026-10-21
---
# chore(e2e): snapshotted workspace volume and grant signer fixtures

## Change

`chore(e2e): snapshotted workspace volume and grant signer fixtures`

## Reason

a Session started in a clone of a snapshotted volume needs, in the e2e lane, a CSI driver with snapshots, a volume holding git clones, its snapshot handle and an issuer of the signed grant the Session is admitted with (giantswarm/kagent-upstream#293). The Kind lane installs the external snapshotter and the CSI hostpath driver (`scripts/kind/setup-csi-snapshots.sh`); `newWorkspaceFixture` clones two repositories of the git fixture into a volume, snapshots it and reads the driver and handle, then moves one origin; `go/core/test/grant` signs ES256 grants and serves their JWKS; `requireNoTokenInSandbox` and `requireNoOriginRequest` are the assertions the Session workspace tests share. Test code and lane setup only

## History

to file with the Session volume source series it serves

The grant signer half is upstream-ready: `go/core/test/grant` (without the providers of the caller-credential series) is part of the admission's upstream-ready commit, branch `upstream/session-volume-admission-2` (`5d3f43d1`), and the lane's grant signer behind a fixed Service with the refusal e2e is branch `upstream/session-admission-e2e` (`3dfdc7086`, stacked on it). The snapshotted workspace volume half (the CSI snapshot lane setup, `newWorkspaceFixture`, `requireNoTokenInSandbox`, `requireNoOriginRequest`) serves the Session's actor mount and waits with `session-the-sessions-actor-with-its-workspace-directory.md` on agent-substrate/substrate#1637.

The actor mount it serves waits on upstream's shared-volume stack, agent-substrate/substrate#3053 on #2828 and #2567 (all open drafts), and on #1988; its exit is that row's.
