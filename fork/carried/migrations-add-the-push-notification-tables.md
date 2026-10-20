---
id: migrations-add-the-push-notification-tables
fork: kagent
theme: compat-migrations
category: compat
status: carried
commits:
  - 'fix(migrations): add the push notification tables to a database on the earlier baseline'
upstream:
  outcome: fork-only
  refs: []
  exit: 'replaced at the conversion re-pin by the fork migration source that converts a v1.20.0 database to the upstream baseline and deletes schema_migrations versions 2 to 6; the file stays until then, since a recorded version with no file fails VerifyMigrated'
  class: 6
  destination: 'giantswarm/kagent-upstream go/core/pkg/migrations, fork source with tracking table fork_schema_migrations'
  first_step: 'at the conversion re-pin, a fork migrations.Source before core whose 000001 converts a v1.20.0-v1.22.x database to the upstream baseline and deletes schema_migrations versions 2-6 (giantswarm/kagent-upstream)'
  source: 'giantswarm @ 45c52c6a9; conversion: new branch'
  test: 'lab upgrade from kagent 1.20.0 with sessions, a read-write share and push registrations; the fork source records version 1 and the controller starts'
  recheck: '2026-11-03, whether a kagent release pins a Substrate carrying agent-substrate/substrate#2888'
  owner: unassigned
  draft: ''
  decision: issuecomment-6301319662
  couplings: [identity-authorizer, credential-injection]
  open_question: 'the conversion maps the old checkpoint scopes FULL to SNAPSHOT_FIDELITY_MEMORY and DATA to SNAPSHOT_FIDELITY_VOLUMES, or nulls them; mapping keeps the resume pointers, which then cold-boot with their files, nulling sends every old checkpoint to a fresh actor and loses its files'
depends_on: []
verified: 2026-10-21
---
# fix(migrations): add the push notification tables to a database on the earlier baseline

## Change

`fix(migrations): add the push notification tables to a database on the earlier baseline`

## Reason

upstream added `session_push_registration`, `session_push_outbox`, their indexes and the unique key `session_task_event_push_source` to the baseline `core/000001_initial.sql` (kagent-dev/kagent#3036) after the line's installations had applied it, so a database migrated before the re-pin onto `main@92dd6caa` (core at version 4) had none of them and every Session operation failed with `relation "session_push_registration" does not exist`. `core/000005` creates each of them as the baseline does, `IF NOT EXISTS`, so a database the current baseline created is unchanged; its Down is empty, the baseline's drops them

## History

fork-only: upstream folds schema changes into its unreleased 1.0 baseline and recreates development databases (the baseline's header); the line has installations on the earlier baseline

2026-10-21: decided in the fork-reduction review of the compat-migrations theme: fork-only (class 6): the next re-pin past kagent-dev/kagent 96c40300 is the first whose Substrate carries agent-substrate/substrate#2888; the fork converts state across it instead of cutting over; a fork migration source with its own tracking table, before core, converts core 1-6 to upstream's baseline (with the share revert and the Session egress move of other themes) and replaces this file.
