---
id: migrations-refuse-to-start-on-a-1-x
fork: kagent
theme: compat-migrations
category: compat
status: carried
commits:
  - 'fix(migrations): refuse to start on a 1.x database instead of migrating it'
upstream:
  outcome: fork-only
  refs: []
  exit: 'folded at the conversion re-pin into the conversion''s stop-version check, which refuses any database other than a fork v1.20.0-v1.24.x one; drops with the conversion at the following re-pin'
  class: 6
  destination: 'the conversion''s stop-version check; giantswarm/agent-platform validateKagentLineCrossing'
  first_step: 'at the conversion re-pin, the conversion''s stop-version check refuses a database that is not core 1-7 with the remedy to step to 1.20.x first (giantswarm/kagent-upstream)'
  source: 'giantswarm @ 44a6eda0c; PreCheck: new branch'
  test: 'PreCheck unit test on a 1.x and a pre-1.20 database; agent-platform render test with a running controller below 1.20.0'
  recheck: 'Substrate GA (agent-substrate/substrate#2004), fallback 2026-11-24, with the conversion re-pin'
  owner: unassigned
  draft: ''
  decision: 'issuecomment-6301319662, amended issuecomment-6339634309'
  couplings: [compat-migrations]
  open_question: ''
depends_on: []
verified: 2026-10-21
---
# fix(migrations): refuse to start on a 1.x database instead of migrating it

## Change

`fix(migrations): refuse to start on a 1.x database instead of migrating it`

## Reason

every 1.x release recorded its baseline as core version 1, the version this tree's baseline carries too, so Goose took a 1.x database for current and the controller started on the `agent_instance` tables. The core source checks for that table before any migration or verification and fails startup with the remedy (`migrations.ErrLegacyBaseline`): a fresh database, the old one kept for retention. The line does not migrate the 1.x schema; each installation's cut-over starts on a fresh database on its existing CNPG cluster (decision of 2026-09-29: conversations, shares, scheduled-run executions and Slack thread bindings are not carried over)

## History

fork-only: upstream has no released baseline to protect

2026-10-21: decided in the fork-reduction review of the compat-migrations theme: fork-only (class 6): the refusal becomes the PreCheck of the conversion's fork migration source, the stop-version kagent 1.20.0, with the chart's line-crossing check refusing a running kagent below it.

2026-10-22: amended in the fork-reduction review of the compat-migrations theme: the conversion re-pin is the first past Substrate GA (fallback 2026-11-24), with one conversion written then against the frozen format; whether it runs as a one-shot agent-platform Job or as fork code for that one re-pin is decided then.
