---
id: fork-test-and-scan-the-consumed-branch-re
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'ci(fork): test and scan the consumed branch, re-pin weekly by workflow, keep a ledger'
  - 'ci(fork): the re-pin checks every carried hunk against the candidate'
  - 'ci(fork): skip the suites on a docs-only pull request'
  - 'ci(fork): a stopped re-pin opens or updates the sync issue'
  - 'ci(fork): mirror upstream''s tags under upstream/ and re-pin onto the ref fork/upstream-ref names'
upstream:
  outcome: fork-only
  refs: []
  exit: 'keep; unblock #709'
depends_on: []
verified: 2026-11-26
---
# ci(fork): test and scan the consumed branch, re-pin weekly by workflow, keep a ledger

## Change

`ci(fork): test and scan the consumed branch, re-pin weekly by workflow, keep a ledger`

## Reason

this document, `ci.yaml`/`image-scan.yaml` on the consumed branch, `sync-upstream.yaml`, the ledger

## History

fork-only
