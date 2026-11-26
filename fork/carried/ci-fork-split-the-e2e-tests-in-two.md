---
id: ci-fork-split-the-e2e-tests-in-two
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'ci(fork): split the e2e tests in two shards'
upstream:
  outcome: fork-only
  refs: []
  exit: 'drops when the line''s e2e job runs on a runner of 8 vCPU or more, the size upstream runs it on'
depends_on: []
verified: 2026-11-26
---
# ci(fork): split the e2e tests in two shards

## Change

`ci(fork): split the e2e tests in two shards`

## Reason

Upstream runs `test-e2e` on an 8-vCPU Blacksmith runner; the line runs it on a 4-vCPU GitHub-hosted runner, where it was the critical path of every CI run (31 to 37 minutes, 19 to 22 of them in `go test`). About half of the test time is in tests that do not call `t.Parallel()` because they restart the controller, so a higher `-parallel` does not shorten it. The matrix now runs two shards, each on a kind cluster of its own, and `scripts/fork/e2e-shard.sh` assigns each top-level test to a shard by a hash of its name, so adding a test never moves another. The repository is public, so the extra leg costs runner time from the org's concurrency, not money.

## History

2026-11-26: carried; fork-only, nothing to file upstream.
