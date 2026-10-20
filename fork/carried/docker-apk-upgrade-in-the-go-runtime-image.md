---
id: docker-apk-upgrade-in-the-go-runtime-image
fork: kagent
theme: ci-publish-infra
category: bugfix
status: carried
commits:
  - 'fix(docker): apk upgrade in the Go runtime image'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#2951, kagent-dev/kagent#3159]
  exit: 'drops at the re-pin onto a commit containing 148814631'
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

2026-10-20: kagent-dev/kagent#3159 merged on 2026-10-13 (`148814631`, not in the pin `92dd6caa`) and upstream's `go/Dockerfile` runs `apk upgrade --no-cache`, so the change is upstream's.
