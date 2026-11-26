---
id: deps-update-compress-for-go-2026-6865
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(deps): update compress to resolve GO-2026-6865'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3474]
  exit: Drop the pin at the next re-pin that includes kagent-dev/kagent#3474 and compress v1.20.3.
depends_on: []
verified: 2026-11-06
---
# Pin compress for GO-2026-6865

## Change

Pin github.com/klauspost/compress to v1.20.2, which fixes the reachable two-block frame panic reported by govulncheck.

## Reason

The consumed upstream pin requires v1.19.2. Upstream merged v1.20.3 in kagent-dev/kagent#3474; the next re-pin carrying that update supersedes this pin.

## History

2026-11-06: verified the upstream dependency update and the v1.20.2 module checksums.
