---
id: fork-refuse-a-new-migration-in-upstreams-core
fork: kagent
theme: compat-migrations
category: infra
status: carried
commits:
  - 'ci(fork): refuse a new migration in upstream''s core source'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3293]
  exit: 'drops at the conversion re-pin past Substrate GA, once the line''s own core migrations are converted away and upstream''s migrations are immutable (kagent-dev/kagent#3293)'
depends_on: []
verified: 2026-10-22
---
# ci(fork): refuse a new migration in upstream's core source

## Change

`ci(fork): refuse a new migration in upstream's core source`

## Reason

upstream edits its unreleased `000001_initial.sql` baseline in place, so every installation's database is converted to upstream's baseline once, at the re-pin past Substrate's GA; each migration the line adds to the `core` source grows that conversion (`000007` was added after the conversion was scoped). `scripts/fork/core-migrations.sh` fails, in the `fork-ledger` check, a core migration the pin does not have unless it is one of the six the line carries today. New schema of the line goes into an extra migration source (`app.Options.ExtraMigrations`)

## History

fork-only: the line's own migration policy; upstream's own `migration-immutability` check (kagent-dev/kagent#3293) guards its files, not the line's
