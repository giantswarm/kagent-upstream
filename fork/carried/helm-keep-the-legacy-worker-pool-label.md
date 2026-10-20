---
id: helm-keep-the-legacy-worker-pool-label
fork: kagent
theme: compat-migrations
category: compat
status: carried
commits:
  - 'fix(helm): keep the legacy worker-pool label on the chart''s WorkerPool'
upstream:
  outcome: to-file
  refs: []
  exit: 'agent-platform sets kagent.dev/worker-pool through substrateWorkerPool.labels (kagent-dev/kagent#3089); the commit drops at the re-pin, the value once no Session is older than kagent 1.11'
  class: 2
  destination: 'kagent chart value substrateWorkerPool.labels, set by giantswarm/agent-platform'
  first_step: 'set kagent.substrateWorkerPool.labels {kagent.dev/worker-pool: <pool>} in agent-platform; move the lab templates to the -name and -namespace labels (giantswarm/agent-platform)'
  source: 'giantswarm @ deb04cb9d; upstream/legacy-worker-pool-label @ f04691703 unused'
  test: 'a lab Session created before the label change resumes after the commit is dropped'
  recheck: 'the conversion re-pin'
  owner: unassigned
  draft: ''
  decision: issuecomment-6301319662
  couplings: [compat-migrations]
  open_question: ''
depends_on: []
verified: 2026-10-21
---
# fix(helm): keep the legacy worker-pool label on the chart's WorkerPool

## Change

`fix(helm): keep the legacy worker-pool label on the chart's WorkerPool`

## Reason

kagent-dev/kagent#3089 (in 1.11) moved placement to `kagent.dev/worker-pool-name` and `kagent.dev/worker-pool-namespace` and the chart stopped stamping `kagent.dev/worker-pool`, which every revision compiled by 1.10 and every Session actor created from it still selects: after the 1.10 → 1.11 upgrade Substrate found no eligible worker for a conversation open across it, answered `no worker has room for the actor`, and its first turn was refused after the controller's 2-minute deferral (giantswarm/kagent-upstream#521). The chart's pool carries the legacy label again beside the two new ones (`substrateWorkerPool.legacyPlacementLabel`, on by default)

## History

to file — prepared in the fork: branch [`upstream/legacy-worker-pool-label`](https://github.com/giantswarm/kagent-upstream/tree/upstream/legacy-worker-pool-label) (`f04691703`, on the mirror `main` @ `d00b4fe6b`, DCO-signed, no fork references, the chart's WorkerPool suite 14/14 there)

2026-10-21: decided in the fork-reduction review of the compat-migrations theme: upstream-has-it (class 2): the chart's substrateWorkerPool.labels carries the legacy label (kagent-dev/kagent#3089 prescribes it), set by agent-platform, so the commit drops at the re-pin and is not filed.
