---
id: helm-controller-agentimage-digest-and-an-optional-default
fork: kagent
theme: harness-variety
category: gs-specific
status: dropped
commits:
  - 'feat(helm): controller.agentImage.digest and an optional default Harness for the chart''s own runtime image'
upstream:
  outcome: declined
  refs: [kagent-dev/kagent#2529, kagent-dev/kagent#2536, kagent-dev/kagent#2952, kagent-dev/kagent#3151]
  exit: 'adopt #3151 at re-pin'
depends_on: [fork-stamp-the-golang-adk-digest-and-substrate]
verified: 2026-11-26
---
# feat(helm): controller.agentImage.digest and an optional default Harness for the chart's own runtime image

## Change

`feat(helm): controller.agentImage.digest and an optional default Harness for the chart's own runtime image`

## Reason

the platform `Harness` of the Go ADK runtime renders from the chart's own digest (`harness.create`, off by default) instead of a digest copied into the meta chart

## History

fork-only — kagent-dev/kagent#2529 (no build-time digest) closed NOT_PLANNED 2026-09-27 and kagent-dev/kagent#2536 (the key this patch uses) closed unmerged: upstream declined the digest twice, so the branch `upstream/helm-default-harness` is not sent; the Harness template is rewritten for #2952's `Agent` shape at the re-pin

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), upstream provides it: kagent-dev/kagent#3151: config.harnessImages (values.yaml:156). The next landed re-pin removes this file
