---
id: api-withdraw-the-harness-turn-limits
fork: kagent
theme: compat-migrations
category: feature
status: carried
commits:
  - 'feat(api): Harness.spec.claude.limits — a budget and a turn cap on every turn of a Claude Code agent'
  - 'feat(helm): harnesses[].limits renders Harness.spec.claude.limits'
  - 'feat(controller): count completed turns by the limit that ended them'
  - 'feat(api): withdraw the Harness turn limits from the API, the Claude harness and the chart'
  - 'test(api): apply the upgraded Harness through Client.Apply'
  - 'fix(helm): ignore harnesses[].limits instead of failing the render'
upstream:
  outcome: open
  refs: [giantswarm/kagent-upstream#615, kagent-dev/kagent#2461, kagent-dev/kagent#3020, kagent-dev/kagent#3307]
  exit: 'the six commits removed as fork commits before any re-pin; the turn usage moves into its own carried commit (giantswarm/kagent-upstream#615)'
  class: 1
  destination: ''
  first_step: 'remove the six commits on the giantswarm branch, keeping the turn usage as its own commit (giantswarm/kagent-upstream)'
  source: 'giantswarm @ fc336a9ab 227f2280e 4cb50b262 14cafa835 369008c06 02b6f74eb'
  test: 'fork CI green; harness-guard-the-turn-usage-on-the stays green'
  recheck: 'the fork commit that removes the six'
  owner: unassigned
  draft: ''
  decision: 'issuecomment-6301319662, amended issuecomment-6339634309'
  couplings: [harness-guard-the-turn-usage-on-the]
  open_question: ''
depends_on: [harness-guard-the-turn-usage-on-the, harness-omp-harness-a-byo-adapter-for, harness-the-codex-turns-usage-on-its-final]
verified: 2026-10-21
---
# feat(api): Harness.spec.claude.limits — a budget and a turn cap on every turn of a Claude Code agent

## Change

`feat(api): Harness.spec.claude.limits — a budget and a turn cap on every turn of a Claude Code agent`, `feat(helm): harnesses[].limits renders Harness.spec.claude.limits`, `feat(controller): count completed turns by the limit that ended them`, `feat(api): withdraw the Harness turn limits from the API, the Claude harness and the chart`, `test(api): apply the upgraded Harness through Client.Apply` and `fix(helm): ignore harnesses[].limits instead of failing the render`

## Reason

withdrawn: the platform bounds no Claude Code turn by a budget or a turn cap, so the last commit removes what the first three added. `Harness.spec.claude` is empty again (CRDs regenerated), the claude compiler and process driver pass no `--max-budget-usd` or `--max-turns`, a result Claude Code reports as `error_max_*` is a failure like any other error, no `stopped_by` is written and the controller's `kagent.agent.turns` counter is gone with its telemetry registry entries. The chart's `harnesses[]` ignores `limits`, so a release whose values still set it upgrades, and the key leaves its values afterwards. What stays: the usage a turn spent (prompt, cached and output tokens, `costUsd`, `numTurns`) under `kagent.dev/a2a/usage` on the final task status of a completed or failed turn. A CRD that still declares `limits` turns a chart's later `claude: {}` into null on a server-side apply (giantswarm/kagent-upstream#501): the CRDs upgrade before or with the chart that drops the key, and an envtest proves the apply after the CRD upgrade

## History

none: kagent-dev/kagent#3020 is to be withdrawn by a person; no agent touches it (still open upstream on 2026-10-19). The next re-pin drops the limits of all six commits together, and this file with them, but not the turn usage the first one carries: the usage, which `harness-the-codex-turns-usage-on-its-final.md` and the omp adapter build on, moves into a carried commit of its own at that re-pin (https://github.com/giantswarm/kagent-upstream/issues/615), and `harness-guard-the-turn-usage-on-the.md` fails the re-pin that loses it

kagent-dev/kagent#2461 merged 2026-10-16 (`9097d0d2`) is the usage extension of the kagent ADK runtimes, which Claude and Codex do not emit, so it is related work and the outcome stays open.

2026-10-21: decided in the fork-reduction review of the compat-migrations theme: drop (class 1): the six net-zero commits leave the line as fork commits before any re-pin, the turn usage moves into its own carried commit, and kagent-dev/kagent#3020 stays open as its author's.

2026-10-21: kagent-dev/kagent#3020 closed by its author: the limits stay withdrawn, as decided in internal issue 38684. The turn usage continues upstream in kagent-dev/kagent#3307 (draft), which reports the Claude and Codex usage through the #2461 extension (`https://kagent.dev/extensions/usage/v1`, task-lifetime tokens per model, no `costUsd` or `numTurns`), not under `kagent.dev/a2a/usage`.

2026-10-22: amended in the fork-reduction review of the compat-migrations theme: the conversion re-pin is the first past Substrate GA (fallback 2026-11-24), with one conversion written then against the frozen format; whether it runs as a one-shot agent-platform Job or as fork code for that one re-pin is decided then.
