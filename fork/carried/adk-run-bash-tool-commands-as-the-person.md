---
id: adk-run-bash-tool-commands-as-the-person
fork: kagent
theme: harness-variety
category: gs-specific
status: carried
commits:
  - 'feat(adk): run bash tool commands as the person of the turn'
  - 'fix(harness/claude): commit as the person of the turn'
  - 'fix(harness): codex and omp commit as the person of the turn too'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3014]
  exit: 'forge literals move to platform Harness configuration after generic shell propagation and real grant checks pass; the generic per-turn commit identity is evaluated separately'
depends_on: [adk-run-bash-tool-commands-on-an-allowlisted, runtime-name-the-agent-and-the-user]
verified: 2026-11-20
---
# feat(adk): run bash tool commands as the person of the turn

## Change

`feat(adk): run bash tool commands as the person of the turn`

## Reason

the allowlisted environment of the bash tool (the row "run bash tool commands on an allowlisted environment") admits neither a git identity nor `GH_TOKEN`, so a coding agent working as a person could not commit as them and the Harness images' `gh` failed every authenticated call (giantswarm/kagent-upstream#290). A command now gets git's author and committer from the person of the turn (`x-kagent-user`; a turn without one gets none, so git's own error stands) and, in a turn whose dispatch names a workspace directory, `GH_TOKEN` set to the inert placeholder `kagent-credential-injected` that the egress gateway replaces with the caller's credential; a real `GH_TOKEN` in the runtime never reaches the command

## History

2026-11-20: kagent-dev/kagent#3014 adopted an allowlist and was closed unmerged by the stale bot. The replacement carries exact Harness literals through serialized ADK shell configuration after baseline filtering; it never passes an arbitrary runtime TOKEN variable. Placeholder values, git credential-helper configuration and GitLab OAuth/host settings belong to the platform. Keep per-turn commit identity separate from authentication. Keep the current helper until replacements pass on each Harness; see docs/architecture/cli-credential-integration.md.
