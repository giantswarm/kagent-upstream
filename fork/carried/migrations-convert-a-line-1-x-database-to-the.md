---
id: migrations-convert-a-line-1-x-database-to-the
fork: kagent
theme: compat-migrations
category: compat
status: carried
commits:
  - 'feat(migrations): convert a line 1.x database to the kagent v1.0.1 schema in the giantswarm track'
  - 'feat(controller): run the migrations alone as a job, with a report before and after'
  - "feat(helm): run the line's migration job before the controller of a release starts"
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3399]
  exit: 'leaves with the 2.x line once every installation runs it: no database of the 1.x line is left to convert. The giantswarm track itself stays while the line carries schema of its own (app.Options.ExtraMigrations)'
depends_on: []
verified: 2026-11-26
---
# feat(migrations): convert a line 1.x database to the kagent v1.0.1 schema in the giantswarm track

## Change

`feat(migrations): convert a line 1.x database to the kagent v1.0.1 schema in the giantswarm track`, `feat(controller): run the migrations alone as a job, with a report before and after` and `feat(helm): run the line's migration job before the controller of a release starts`

## Reason

A database the line's 1.x releases migrated holds core versions 1 to 7: upstream's `000001` as it was at the previous pin `92dd6caa` and the line's own `000002` to `000007`. Upstream's core track at v1.0.1 has `000001` edited in place, `000002_runtime_bootstrap` and `000003_session_revert_operation`; goose applies no version at or below the highest one a database holds, so the core track does nothing there, the controller fails on the missing columns at runtime, and every later upstream core version up to 7 (upstream `main` already has `000004_push_signing_state`) would be skipped silently. Stored bytes changed too: `Session` field 15 is `AgentProvenance` in `kagent.api.v1` (a 1.x row fails to decode), `SessionShare` was renumbered by kagent-dev/kagent#3189 and lost its permission, the push outbox and the snapshot content scope are gone, and long-term memory is keyed `<namespace>/<name>`.

The conversion is the line's giantswarm track, registered through upstream's `app.Options.ExtraMigrations`, migration `000003`, one transaction: upstream's deltas, the snapshot fidelity (`VOLUMES`, what the Substrate 2.x line converts every snapshot to), the share permission in `session_share_scope`, the proto rewrites in PL/pgSQL, one pending push delivery per registration with undelivered outbox rows, the memory keys, and core's tracking table left at upstream's versions 1 to 3. It also keeps the line's own schema on every database (the quiescence lease, the fencing sequence, `agent_deleted`). `go/core/pkg/lineconvert` reports counts and encoding-independent digests; `-migrate-only` runs it before and after the migrations and fails on a difference, and the chart's `migrationJob` runs that as a hook after stopping the previous controller.

## History

2026-11-26: written for the re-pin onto kagent v1.0.1.
