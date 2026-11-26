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
  exit: 'replaced at the conversion re-pin by the conversion that converts a v1.20.0 database to the upstream baseline and deletes schema_migrations versions 2 to 7; the file stays until then, since a recorded version with no file fails VerifyMigrated'
  class: 6
  destination: 'the conversion of the conversion re-pin: a one-shot agent-platform Job or fork code, decided at GA'
  first_step: 'at the conversion re-pin, a conversion that converts a v1.20.0-v1.24.x database to the upstream baseline and deletes schema_migrations versions 2-7 (giantswarm/kagent-upstream)'
  source: 'giantswarm @ 45c52c6a9; conversion: new branch'
  test: 'lab upgrade from kagent 1.20.0 with sessions, a read-write share and push registrations; after the conversion VerifyMigrated passes and the controller starts'
  recheck: 'Substrate GA (agent-substrate/substrate#2004), fallback 2026-11-24'
  owner: unassigned
  draft: ''
  decision: 'issuecomment-6301319662, amended issuecomment-6339634309'
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

2026-10-22: amended in the fork-reduction review of the compat-migrations theme: the conversion re-pin is the first past Substrate GA (fallback 2026-11-24), with one conversion written then against the frozen format; whether it runs as a one-shot agent-platform Job or as fork code for that one re-pin is decided then.
