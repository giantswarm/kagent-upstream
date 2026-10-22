---
id: migrations-move-an-installations-tables-from-public
fork: kagent
theme: compat-migrations
category: feature
status: carried
commits:
  - 'feat(migrations): move an installation''s tables from public into the configured schemas'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3111]
  exit: 'dropped at the conversion re-pin; not filed upstream (kagent-dev/kagent#3111, #3160 and #3226 support fresh databases only)'
  class: 1
  destination: ''
  first_step: 'drop at the conversion re-pin (giantswarm/kagent-upstream)'
  source: 'giantswarm @ 200c2cd41; upstream/schema-migration-from-public @ c806ad1c3 unused'
  test: 'the conversion lab upgrade finds no kagent table in public'
  recheck: 'the conversion re-pin'
  owner: unassigned
  draft: ''
  decision: 'issuecomment-6301319662, amended issuecomment-6339634309'
  couplings: [compat-migrations]
  open_question: ''
depends_on: []
verified: 2026-10-21
---
# feat(migrations): move an installation's tables from public into the configured schemas

## Change

`feat(migrations): move an installation's tables from public into the configured schemas`

## Reason

upstream moved kagent's tables to a `kagent` schema and pgvector to `extensions` (kagent-dev/kagent#3111) with no migration of existing tables, so an installation upgraded onto it would start on empty tables and lose every Agent's sessions, conversations and memories. At start, before the migration prechecks, the controller and `kagent db migrate` move the tables, views, sequences and both tracking tables from `public` into `database.postgres.schema` and, with vectors enabled, pgvector into `vectorSchema`, in one transaction under the core migration lock; a database already in the configured schemas is untouched, one split between both schemas is refused (`ErrHalfMoved`), and `VerifyMigrated` refuses both with migrations skipped. No down path: the chart README says how to move the tables back before a rollback (giantswarm/kagent-upstream#456)

## History

to file — prepared in the fork: branch `upstream/schema-migration-from-public` (mirror `main` @ the pin `92dd6caa` + the patch, `ab57503b`, DCO-signed, no fork references; its message is the pull-request text), the upgrade path #3111 left out. kagent-dev/kagent searched 2026-10-13: no issue or pull request moving existing tables out of `public`

kagent-dev/kagent#3111 merged 2026-10-09 (`5e14c16c`, in the pin) is the schema move this change adds the upgrade path for, not its exit, so it stays in `refs` as related work and the outcome stays to-file.

2026-10-21: decided in the fork-reduction review of the compat-migrations theme: drop (class 1): every installation has moved its tables, upstream supports fresh databases only, so the commit drops at the conversion re-pin and the prepared branch is not filed.

2026-10-22: amended in the fork-reduction review of the compat-migrations theme: the conversion re-pin is the first past Substrate GA (fallback 2026-11-24), with one conversion written then against the frozen format; whether it runs as a one-shot agent-platform Job or as fork code for that one re-pin is decided then.
