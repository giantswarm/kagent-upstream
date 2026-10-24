---
id: api-workspace-volume-reference-and-admission-token
fork: kagent
theme: workspace-volumes
category: feature
status: carried
commits:
  - 'feat(api): workspace volume reference and admission token on CreateSession'
upstream:
  outcome: to-file
  refs: [agent-substrate/substrate#1637, kagent-dev/kagent#3153, kagent-dev/kagent#3155, kagent-dev/kagent#3156, kagent-dev/kagent#3339]
  exit: 'after agent-substrate/substrate#2567 lands, reshaped onto its shared volumes'
depends_on: [giantswarm/substrate:existing-volume-mounted-per-actor-at-sub, giantswarm/substrate:read-write-mounts-missing-sub-path-directory]
verified: 2026-10-24
---
# feat(api): workspace volume reference and admission token on CreateSession

## Change

`feat(api): workspace volume reference and admission token on CreateSession`

## Reason

the platform starts a Session in its own directory of a workspace's shared volume: `CreateSessionRequest.volume_source` names an existing read-write-many volume (CSI driver and handle) and its mounts (a clean relative sub-path of the volume, `${SESSION_ID}` standing for the Session's id, at a clean absolute path outside `/data`, at most one read-write: the Session's own directory, the workspace's mirrors read-only), and `admission_token` carries the proof the installation's admission verifies; kagent never creates or deletes the volume. The source is stored on the Session row and shown on get and list; the token is handed to the admission and stored or shown nowhere; a retry under the same `request_id` must name the same source. A request without the fields is handled exactly as before. Until a `session.VolumeAdmission` is configured (the next patch, the `jwt` admission of the workspace grant), every request that names a source or carries a token is refused `FailedPrecondition` "this installation admits no Session volume", so no caller mounts a volume of its choosing; an `Agent` or `AgentTemplate` carries no volume, which the envtest proves against the shipped CRDs. API and storage only: the admission and the actor's mounts follow

## History

open upstream as kagent-dev/kagent#3155 (branch `upstream/session-volume-source`, off upstream `main`): the same API, storage and refusal; the source is `Session.volume_source` (field 17) as here, stored for the actor mount that follows once Substrate can mount an existing volume (agent-substrate/substrate#1637); drops at the re-pin onto an upstream with #3155. #3155 conflicts with upstream `main` since kagent-dev/kagent#3153; its rebased head is prepared as branch `upstream/session-volume-source-3` (`258bef34f`, on the mirror `main` @ `b42a6c53a`), for its owner to move the pull request to Rebased onto upstream `main` @ `7a0a4fd0` and folded with the jwt admission into one upstream-ready commit as branch `upstream/session-volume-admission-2` (`5d3f43d1`, on upstream `main` @ `2261632c`, Substrate v0.6.0-alpha3; DCO-signed, no fork references, `make -C go test` green there); it supersedes the heads above, and a human decides whether it replaces kagent-dev/kagent#3155 and #3156.

kagent-dev/kagent#3153 merged 2026-10-13 (`16e63ca5`) is the configuration change #3155 conflicts with, not this change's exit, so it stays in `refs` as related work.

kagent-dev/kagent#3155 and #3156 were closed upstream unmerged on 2026-10-20: upstream's maintainers prefer Substrate's shared volumes to a per-Session volume reference, and the work returns once agent-substrate/substrate#2567 lands. The upstream-ready commit stays prepared as branch `upstream/session-volume-admission-2` (`5d3f43d1`) with its e2e on `upstream/session-admission-e2e` (`3dfdc7086`); it is reshaped onto Substrate's shared-volume API before a human files it again.

2026-10-24: kagent-dev/kagent#3339 (open) adds `sessionsvc.Service.CreateFromRequest`, the single mapping of `CreateSessionRequest` onto `Create` that the gRPC server and the core MCP server's new `create_session` share. At the re-pin past it, this line's `CreateRequest` mapping (`volume_source`, `admission_token`, `bind_actor`, `tool_images`) moves from `grpcserver/session.go` into `CreateFromRequest`; `create_session` then carries those fields with no change to the MCP code, its input schema derived from the request descriptor.
