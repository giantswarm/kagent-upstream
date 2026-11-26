---
id: controller-resolve-a-modelconfig-whose-secret
fork: kagent
theme: credential-injection
category: test
status: carried
commits:
  - 'test(controller): resolve a ModelConfig whose Secret is created after it'
upstream:
  outcome: to-file
  refs: []
  exit: file the branch
depends_on: []
verified: 2026-11-08
---
# test(controller): resolve a ModelConfig whose Secret is created after it

## Change

`test(controller): resolve a ModelConfig whose Secret is created after it`

## Reason

a consumer that creates a ModelConfig before its API key Secret needs `ResolvedRefs` to follow the Secret's creation, not a failure requeue (giantswarm/kagent-upstream#239). The controller already watches a ModelConfig's referenced Secrets: `ResolveModelConfig` fetches each through `krt.FetchOne` on the Secret informer, which registers the dependency even while the Secret is missing, so the Secret's creation re-resolves the ModelConfig and the status write follows within milliseconds. The delays seen on the 1.2 line (about a second, and about 80 s after repeated misses) were the status write, not the watch: an `UpdateStatus` of the informer's copy failed with a Conflict and went back through the queue's exponential backoff with a ten-attempt budget. `controller-patch-the-harness-and-modelconfig-status-without` (a merge patch without a resourceVersion) and upstream kagent-dev/kagent#2994 (retries without an attempt budget) removed both on this line. The test drives the real informer graph (`NewCollections` over a fake client), for all namespaces and for a watched namespace, requires `ResolvedRefs=True` within the queue's first one-second backoff, and fails when the Secret lookup stops registering the dependency

## History

to file — prepared in the fork: branch `upstream/modelconfig-secret-created-after`
