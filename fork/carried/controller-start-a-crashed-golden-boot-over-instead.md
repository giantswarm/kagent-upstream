---
id: controller-start-a-crashed-golden-boot-over-instead
fork: kagent
theme: golden-boot
category: bugfix
status: dropped
commits:
  - 'fix(controller): start a crashed golden boot over instead of failing the Agent for good'
  - 'fix(controller): fail a crashed golden boot at once instead of starting it over'
upstream:
  outcome: drops-at-repin
  refs: [kagent-dev/kagent#2994]
  exit: the next re-pin folds the carry and its removal out
depends_on: []
verified: 2026-10-27
---
# fix(controller): start a crashed golden boot over instead of failing the Agent for good

## Change

`fix(controller): start a crashed golden boot over instead of failing the Agent for good`

## Reason

a golden boot that ends `GoldenActorCrashed` was terminal in the controller: Substrate (agent-substrate/substrate#1220) crashes the actor on every failed atelet Run and the template reconciler fails the ActorTemplate once with the recorded crash, and the controller only carried that verdict into `Ready=False` — so a worker pod replaced under the golden actor (the WorkerPool rolling while a Harness change recompiles every template, as in the 4.7.19 → 4.8.0 upgrade) left every AgentTemplate unrunnable until something else changed the Harness. The crash does not tell an infrastructure cause from a workload that dies on every boot, so the controller retries every `GoldenActorCrashed` and lets the budget tell them apart. It deletes the crashed ActorTemplate with its golden actor and creates the desired one again, up to five times per revision with a doubling backoff (20 s, 40 s, 80 s, then 2 min), reports `Ready=False ActorTemplateRetrying` meanwhile and `ActorTemplateFailed` with the count once the budget is spent; any other failure (`GoldenActorInvalid`, `GoldenActorUnexpectedState`) stays final at once. Substrate carries no golden-boot bound of its own since the v0.3.0-alpha1 re-pin (`maxGoldenBootRetries = 5` in `go/core/internal/controller/reconciler.go`, message `golden boot N of 6 failed (...)`)

## History

superseded structurally by kagent-dev/kagent#2994 (`cb6b756c`: the queue owns retries, a golden failure stays terminal), so the patch cannot rebase; the semantics are carried on #2994 (`098f63e70`, `6fc3b7302`) and go upstream from there

dropped: a crashed golden boot fails the Agent at once again, as upstream keeps it (kagent-dev/kagent#2994). The retry hid a Harness adapter's deterministic refusal behind six boots, about 11 minutes (giantswarm/kagent-upstream#922's agentlab proof). The causes it covered are retried closer to them: a worker replaced under the golden actor by Substrate's `actortemplate-reconciler-retries-golden-take-rolling-worker` and `-whose-golden`, a transient skill or plugin fetch failure by `fix(skills): retry a git fetch on a transient network failure`. `fix(controller): fail a crashed golden boot at once instead of starting it over` removes it; the next re-pin folds the carry and its removal out
