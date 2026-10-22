---
id: ci-fork-a-pull-request-closes-no-issue-by-keyword
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'ci(fork): a pull request closes no issue by keyword'
upstream:
  outcome: fork-only
  refs: []
  exit: keep
depends_on: []
verified: 2026-10-22
---
# ci(fork): a pull request closes no issue by keyword

## Change

`ci(fork): a pull request closes no issue by keyword`

## Reason

GitHub closes the issue a closing keyword names in a pull request's description or in a commit it lands, upstream issues included, and ledger descriptions quote upstream pull requests: a Substrate-line ledger description quoting "Fixes #2555" closed agent-substrate/substrate#2555 when it merged. The `fork-closing-keywords` workflow (`scripts/fork/closing-keywords.sh`) fails a pull request whose title, description or commits pair a closing keyword with an issue reference, and re-runs when the description is edited

## History

fork-only, never upstream
