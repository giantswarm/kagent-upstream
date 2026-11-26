---
id: helm-render-the-charts-harness-on-api-kagent
fork: kagent
theme: chart-knobs
category: gs-specific
status: dropped
commits:
  - 'fix(helm): render the chart''s Harness on api.kagent.dev/v1alpha3 without the admission selector'
upstream:
  outcome: fork-only
  refs: []
  exit: keep until upstream chart renders one
depends_on: []
verified: 2026-11-26
---
# fix(helm): render the chart's Harness on api.kagent.dev/v1alpha3 without the admission selector

## Change

`fix(helm): render the chart's Harness on api.kagent.dev/v1alpha3 without the admission selector`

## Reason

upstream kagent-dev/kagent#2952 moved the API to `api.kagent.dev/v1alpha3` and replaced the admission labels by an explicit `Agent` whose `harnessRef` names its Harness, so the chart's Harness (`harness.create`) renders on the new group with no `selector`, and the meta chart's override of the selector key becomes a no-op

## History

fork-only

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), upstream provides it: harnesses.enabled renders default Harnesses (helm/kagent/templates/harnesses.yaml:1). The next landed re-pin removes this file
