---
id: telemetry-fetch-the-contracts-upstream-sources-once
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'ci(telemetry): fetch the contract''s upstream sources once at their pins'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3442, kagent-dev/kagent#3484]
  exit: 'kagent-dev/kagent#3484 merged, then the re-pin drops the commit'
  class: 4
  destination: 'kagent-dev/kagent: an issue first (the change is over the 100-line threshold of CONTRIBUTING.md:79), then a pull request; facts in the fork-ledger upstream fact sheet ci-publish-infra'
  first_step: 'kagent-dev/kagent#3484 reviewed and merged'
  source: 'upstream/telemetry-deps-2@6ca1fc98c on kagent-dev/kagent main 692964d30: the carried commit re-cut with the six requirements of kagent-dev/kagent#3442'
  test: 'after make semconv-deps, make semconv-verify passes with github.com unreachable; upstream main makes 17 remote Weaver fetches per make semconv-verify'
  recheck: '2026-11-17 (kagent-dev/kagent#3484''s review)'
  owner: unassigned
  draft: issuecomment-6301360735
  decision: issuecomment-6301358702
depends_on: []
verified: 2026-11-02
---
# ci(telemetry): fetch the contract's upstream sources once at their pins

## Change

`ci(telemetry): fetch the contract's upstream sources once at their pins`

## Reason

the Telemetry Contract Check cloned three GitHub repositories on every Weaver run (the two registries the manifest depends on, the GenAI one re-cloning the core one through its own manifest, and the Weaver policy packages), eleven clones per `make semconv-verify`; one flaked clone turned the check red on a pull request's first run (giantswarm/kagent-upstream#192). The sources are pinned in `telemetry/versions.env` and fetched once by `make semconv-deps` into a git-ignored checkout the manifest and the check read from disk, cached in CI keyed on the pins; Weaver's schema URL overrides cannot avoid the clone in v0.26.1 or v0.27.0, whose loader clones a manifest dependency before consulting them

## History

to file — prepared in the fork: branch `upstream/telemetry-deps` (mirror `main` + the patch, DCO-signed, no fork references)

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): upstream issue, then PR, on kagent-dev/kagent. Measured on upstream `96c40300` with Weaver v0.27.0: 17 remote fetches per `make semconv-verify` (5 for the check, 3 for each of four generate runs), not eleven.

2026-10-29: filed as kagent-dev/kagent#3442 by fiunchinho from the draft, with the `Makefile` and `ci.yaml` references refreshed to upstream `main` `d9cc3a826`; the pull request follows its answer on a new branch, in a slot.

2026-10-30: krisztianfekete confirmed kagent-dev/kagent#3442 with six requirements: pin the core registry by commit and compare the checkout with the pin; refuse a GenAI registry whose core dependency names another release; prove offline after the fetch; `live-check.sh` fetches the deps itself; the deps inside the repository for the Weaver container; the fetch script in the cache key.

2026-11-02: filed as kagent-dev/kagent#3484 by fiunchinho from `upstream/telemetry-deps-2` (`6ca1fc98c`, +175/-10), the carried commit re-cut on upstream `main` `692964d30` with the six requirements: every pin is a commit, the core pin declares the tag the GenAI registry names and the fetch checks that the tag still names the commit, CI runs `semconv-verify` with the Weaver container on `--network none`, `actions/cache` is SHA-pinned and keyed on `versions.env` and `deps.sh`; the stray conflict marker the carried commit left in `.gitignore` is gone. The outcome stays open on the PR.
