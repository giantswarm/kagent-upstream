---
id: session-refuse-a-share-on-a-session-whose-harness-runs-code
fork: kagent
theme: identity-authorizer
category: feature
status: carried
commits:
  - 'feat(session): refuse a share on a Session whose Harness runs code'
  - 'fix(session): refuse a share unless the Session''s Harness runs the kagent runtime'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3189]
  exit: 'class 6 fork-only, rides a2agateway-run-a-turn-a-share: the refusal protects writable shares on the harnesses that keep a working directory; upstream''s read-only shares cannot run a turn (go/core/internal/service/session/service.go:413 on kagent-dev/kagent main 96c40300), so upstream has nothing to refuse. First step: none; the per-sender isolation question goes into the multi-party sessions issue of that row. Test: the platform lab''s coding test share stage. Dependencies: a2agateway-run-a-turn-a-share. Exit: with that row. Re-check: at each re-pin. Owner: unassigned'
depends_on: []
verified: 2026-10-20
---
# feat(session): refuse a share on a Session whose Harness runs code

## Change

`feat(session): refuse a share on a Session whose Harness runs code` and `fix(session): refuse a share unless the Session's Harness runs the kagent runtime`

## Reason

a coding agent's runtime keeps what a turn leaves behind (a repository hook, a build target), so a share of its Session would run one sender's leftovers under the next sender's credentials; the platform's gateway refuses such shares on its own surfaces only, while `CreateSessionShare` let a creator mint one directly. `CreateSessionShare` reads the Session's Agent and its Harness (inline or `harnessRef`) and refuses the share, read-only and read-write alike, with `FailedPrecondition` and a message naming the remedy (a Session of one's own) unless the Harness is the `kagent` runtime: `claude`, `codex` and `byo` are refused, and an Agent or Harness that cannot be read refuses the share too, so the rule is fail-closed and needs no API field; reads, sends and existing shares are unchanged. The rule is derived from the runtime because a Harness field is one upstream will not carry (kagent-dev/kagent#3065 drops read-write shares) and one an inline Harness could leave out. It does not cover the `kagent` runtime's own file and bash tools, which can also leave files behind in a Session's working directory. Delete this patch when per-sender isolation runs each sender's turn apart

## History

to file — prepared in the fork: branch `upstream/session-share-runs-sender-code` (mirror `main` at the pin `6c4cfb2b` + the patch, no API change, DCO-signed, no fork references); no upstream pull request found for a share refusal on a Harness

2026-10-20: decided in the fork-reduction review of theme T9 (findings comment of that date on the theme issue, internal): class 6, rides the writable-share row; upstream's read-only shares make it moot there.
