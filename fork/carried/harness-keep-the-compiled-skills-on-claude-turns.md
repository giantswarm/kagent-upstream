---
id: harness-keep-the-compiled-skills-on-claude-turns
fork: kagent
theme: harness-variety
category: bugfix
status: carried
upstream:
  outcome: superseded
  refs: [kagent-dev/kagent#3203, giantswarm/kagent-upstream#685, kagent-dev/kagent#3191]
  exit: drops at next re-pin
depends_on: []
commit: b2a762630
verified: 2026-10-19
---
| Carried commit (subject) | Why the platform needs it | Upstream |
|---|---|---|
| `fix(harness): keep the compiled skills on Claude turns that need approval` | Claude Code counts skills under an `--add-dir` directory as part of the project setting source, so the `--setting-sources ""` the driver passed on approval turns dropped every compiled skill (Claude Code 2.1.260); the driver now passes only `--settings` and `--permission-prompt-tool`, and the rendered `ask` rules still hold over user and project `allow` rules | to file |
