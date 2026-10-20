---
id: docker-apk-upgrade-in-the-go-runtime-image
fork: kagent
theme: ci-publish-infra
category: bugfix
status: carried
commits:
  - 'fix(docker): apk upgrade in the Go runtime image'
upstream:
  outcome: superseded
  refs: [kagent-dev/kagent#2951, kagent-dev/kagent#3159]
  exit: 'drops at the re-pin onto a commit containing 14881463'
  class: 2
  destination: 'kagent-dev/kagent#3159 (148814631): go/Dockerfile:38 at 96c40300 runs the same apk upgrade'
  first_step: 'leave the commit out at the next re-pin past 96c40300'
  test: 'the re-pinned controller and golang-adk image builds run apk upgrade --no-cache; scripts/fork/carried.sh --check passes with this file deleted'
  recheck: 'the next re-pin'
  owner: unassigned
  decision: issuecomment-6301358702
depends_on: []
verified: 2026-10-20
---
# fix(docker): apk upgrade in the Go runtime image

## Change

`fix(docker): apk upgrade in the Go runtime image`

## Reason

the same Alpine finding in the controller and Go ADK images (`go/Dockerfile`), which #2756 did not cover

## History

to file (upstream's `go/Dockerfile` still has no `apk upgrade`; kagent-dev/kagent#2951 pins packages in `ui/Dockerfile` only)

kagent-dev/kagent#3159 merged 2026-10-13 (`14881463`, not in the pin `92dd6caa`) adds the same `apk upgrade` to `go/Dockerfile`, so the outcome is superseded; kagent-dev/kagent#2951 (the UI Dockerfile only) stays in `refs` as related work.

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): upstream has it (kagent-dev/kagent#3159); the outcome stays superseded.
