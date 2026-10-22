---
id: telemetry-fetch-the-contracts-upstream-sources-once
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'ci(telemetry): fetch the contract''s upstream sources once at their pins'
upstream:
  outcome: to-file
  refs: []
  exit: 'the kagent-dev/kagent issue answered and its pull request merged, then the re-pin drops the commit'
  class: 4
  destination: 'kagent-dev/kagent: an issue first (the change is over the 100-line threshold of CONTRIBUTING.md:79), then a pull request; facts in the fork-ledger upstream fact sheet ci-publish-infra'
  first_step: 'file the kagent-dev/kagent issue from its draft'
  source: 'new branch on kagent-dev/kagent main after the issue; upstream/telemetry-deps@41a352d8b is stale (118 commits behind, 4 conflicts, predates kagent-dev/kagent#3107, actions/cache not SHA-pinned)'
  test: 'after make semconv-deps, make semconv-verify passes with github.com unreachable; upstream main makes 17 remote Weaver fetches per make semconv-verify'
  recheck: '2026-11-17 (the issue''s reaction)'
  owner: unassigned
  draft: issuecomment-6301360735
  decision: issuecomment-6301358702
depends_on: []
verified: 2026-10-20
---
# ci(telemetry): fetch the contract's upstream sources once at their pins

## Change

`ci(telemetry): fetch the contract's upstream sources once at their pins`

## Reason

the Telemetry Contract Check cloned three GitHub repositories on every Weaver run (the two registries the manifest depends on, the GenAI one re-cloning the core one through its own manifest, and the Weaver policy packages), eleven clones per `make semconv-verify`; one flaked clone turned the check red on a pull request's first run (giantswarm/kagent-upstream#192). The sources are pinned in `telemetry/versions.env` and fetched once by `make semconv-deps` into a git-ignored checkout the manifest and the check read from disk, cached in CI keyed on the pins; Weaver's schema URL overrides cannot avoid the clone in v0.26.1 or v0.27.0, whose loader clones a manifest dependency before consulting them

## History

to file — prepared in the fork: branch `upstream/telemetry-deps` (mirror `main` + the patch, DCO-signed, no fork references)

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): upstream issue, then PR, on kagent-dev/kagent. Measured on upstream `96c40300` with Weaver v0.27.0: 17 remote fetches per `make semconv-verify` (5 for the check, 3 for each of four generate runs), not eleven.
