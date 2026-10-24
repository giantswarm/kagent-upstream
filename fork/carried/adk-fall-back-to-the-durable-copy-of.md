---
id: adk-fall-back-to-the-durable-copy-of
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(adk): fall back to the durable copy of a skill a restart cannot fetch'
upstream:
  outcome: superseded
  refs: [kagent-dev/kagent#3203, kagent-dev/kagent#3258]
  exit: '#3099 merges; drops at the re-pin past it'
depends_on: [skills-retry-a-git-fetch-on-a-transient-network, skills-retry-an-http-2-stream-or-cut-transfer]
verified: 2026-10-24
---
# fix(adk): fall back to the durable copy of a skill a restart cannot fetch

## Change

`fix(adk): fall back to the durable copy of a skill a restart cannot fetch`

## Reason

a restored Go ADK actor starts with an empty root filesystem and materializes its Agent Plugin resources again before it serves `/readyz`; one source it could not fetch ended the process (`materialize Agent Plugins: ... exit status 128`), the wakeup probe timed out after 30 s and Substrate marked the actor crashed, so the conversation on its durable volume was lost, although the skill had been fetched at the earlier start (giantswarm/kagent-upstream#541). Every fetched skill or plugin root is now copied to `/data/agent-plugins` on the durable volume, keyed by the exact source, version included; a restart whose fetch fails uses the copy of the same source and logs it, a start without a copy of that exact source (the first start, a new commit) fails as before, so a pin is never served by the version it replaced, and copies no longer named are pruned. A restart on the artifact it already ran keeps the conversation; a crashed actor loses it

## History

to file — prepared in the fork: branch `upstream/adk-skill-durable-copy-exact` (mirror `main` @ `92dd6caa` + the patch, DCO-signed, no fork references)

kagent-dev/kagent#3203 merged 2026-10-16 (`673d181e`): it mounts OCI skills through Substrate image volumes and leaves git sources fetched at start, so it stays in `refs` as the mechanism the exit relies on and the outcome stays to-file.

2026-10-24: superseded by kagent-dev/kagent#3099 (open). Verified in its diff: `config.MaterializeAgentPlugins` returns an `EnsureSkills` closure, standalone skill fetches get a 30 s best-effort budget at start with a warning instead of a process exit, and the A2A executor installs anything still missing before each task, failing that task with `skills_unavailable:` while the next one retries. A restored actor whose fetch fails therefore keeps its conversation, which was this commit's reason. Two gaps stay: #3099 defers standalone skills only (a git-sourced plugin package still fails the start), and an actor with no egress at all serves failing tasks instead of the copy it had. Neither warrants a competing PR; the commit stays carried until the re-pin past #3099.
