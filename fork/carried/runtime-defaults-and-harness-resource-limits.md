---
id: runtime-defaults-and-harness-resource-limits
fork: kagent
theme: harness-variety
category: feature
status: carried
commits:
  - 'feat(runtime): apply controller defaults and per-Harness resource limits'
  - 'fix(runtime): check pinned toolset limits before preparation'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3179, kagent-dev/kagent#3625]
  exit: 'Drop the sizing backport when the upstream pin contains runtimeDefaults plumbing and the merged per-Harness resource API from #3625.'
depends_on: []
verified: 2026-11-20
---
# Runtime defaults and per-Harness resource limits

## Change

Controller CPU and memory defaults and optional `Harness.spec.resources` resolve independently into compiled revisions, revision identity and ActorTemplate limits. Session tool templates copy these limits. Persisted unsized revisions retain their original identity and template contents. The lifecycle adapter refuses automatic repoints between templates with different resources so existing Sessions keep their compute envelope, including on Resume or the idle sweep. A toolset checks the pinned template's identity and limits before preparing a replacement, so refused moves create no unrecorded golden snapshots.

## Reason

The consumed pin predates upstream's sizing path. Backport the `runtimeDefaults` CPU and memory mechanism and the approved semantics of kagent-dev/kagent#3625 at `d262f03fe16daf7b2c5f899ff398f33703fd1a81`, without the unrelated typed configuration, bootstrap or Substrate API migrations. The fork's environment registry bridges `config.runtimeDefaults` chart values to compiler inputs. The existing Substrate contract carries quantity strings; the adapter validates the same positive limits and upper bounds as upstream before constructing them.

## History

2026-11-20: kagent-dev/kagent#3625 is open with approval on its current head. Upstream main contains controller defaults, revision hashing and actor resource construction. No canonical upstream mutation is needed for this backport.
