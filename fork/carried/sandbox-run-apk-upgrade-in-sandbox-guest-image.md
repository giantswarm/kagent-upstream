---
id: sandbox-run-apk-upgrade-in-sandbox-guest-image
fork: kagent
theme: ci-publish-infra
category: bugfix
status: carried
commits:
  - 'fix(sandbox): run apk upgrade in sandbox-guest image'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3664]
  exit: 'drops at the re-pin onto a commit containing 98fc5eb0'
depends_on: []
verified: 2026-11-26
---
# fix(sandbox): run apk upgrade in sandbox-guest image

## Change

`fix(sandbox): run apk upgrade in sandbox-guest image`

## Reason

The sandbox-guest image only ran `apk add`, so it shipped the Alpine base image's package versions. Trivy's image scan failed on CVE-2026-124709 (HIGH, `libcrypto3` and `libssl3` 3.5.9-r0, fixed in 3.5.9-r2), which made `scan-ok` red on every pull request against `giantswarm`. The image now runs `apk upgrade --no-cache` inside the existing retry loop, as `go/Dockerfile` does. Upstream made the same change in kagent-dev/kagent#3664 (`98fc5eb0`), after the line's pin; the commit carries upstream's subject.

## History

2026-11-26: carried. Upstream merged the change in kagent-dev/kagent#3664; nothing to file.
