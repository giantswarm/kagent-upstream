---
id: adk-load-a-skill-md-whose-frontmatter-carries
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(adk): load a SKILL.md whose frontmatter carries fields outside the agentskills.io set'
upstream:
  outcome: open
  refs: [google/adk-go#1957, google/adk-go#1958]
  exit: 'google/adk-go#1958 merges and a v2 release carries it; kagent bumps google.golang.org/adk/v2 (go/go.mod) past it, then this commit and go/adk/pkg/skillfs drop'
depends_on: []
verified: 2026-10-24
---
# fix(adk): load a SKILL.md whose frontmatter carries fields outside the agentskills.io set

## Change

`fix(adk): load a SKILL.md whose frontmatter carries fields outside the agentskills.io set`

## Reason

the Go ADK's `skill.Parse` decodes the `SKILL.md` frontmatter with `KnownFields(true)` and knows the agentskills.io fields only, so one field outside that set — Claude Code's `user-invocable`/`argument-hint`, a top-level `version:` — failed the whole agent's golden boot (`failed to load skills: invalid frontmatter … field user-invocable not found in type skill.Frontmatter`); on a production installation's cut-over, agents whose skills carried Claude Code's fields or a top-level `version:` stayed unrunnable while the same skills had loaded on kagent 0.10's Python runtime, whose `Frontmatter` allows extra fields. The runtime now hands the ADK a filesystem view (`go/adk/pkg/skillfs.Lenient`) in which every skill's `SKILL.md` carries only the fields the ADK knows; the ADK's parser, validation (`name`/`description` required, directory name must match) and resource rules stay the single authority, `metadata` is untouched, and a `SKILL.md` the filter cannot read as frontmatter is served unchanged so the ADK reports it as before

## History

to file — prepared in the fork: branch `upstream/skills-lenient-frontmatter` (mirror `main` @ the pin `6a6b54ff` + the patch, `84fb5872`, DCO-signed, no fork references; its message is the pull-request text). google/adk-go searched 2026-09-13: no issue or PR on unknown frontmatter fields (`main` still `KnownFields(true)`; #1296/#1301 fixed the scalar `allowed-tools` form only), the agentskills.io reference validator rejects unknown fields while adk-python's `Frontmatter` allows them; kagent-dev/kagent has nothing on it. The ADK-side fix (decode without `KnownFields`) is a candidate for google/adk-go once the team reviews the queue

2026-10-24: filed in google/adk-go as issue #1957 and PR #1958 (+22/−5: `Parse` decodes without `KnownFields(true)`, the former `unknown fields` rejection test moved to the valid table with `user-invocable`, `argument-hint` and a top-level `version`). The issue also asks whether `ListFrontmatters` should tolerate one malformed SKILL.md instead of failing the whole toolset; the PR stands under either answer. `KnownFields(true)` dates from the package's first commit (090cbe58, #693) and no later change defends it; adk-python's `Frontmatter` is `extra="allow"` and adk-go's CONTRIBUTING names adk-python the source of truth. The earlier `kagent-dev/kagent#1296` ref in this row was mis-resolved (google/adk-go#1296 is the scalar `allowed-tools` issue) and was already dropped in #748.
