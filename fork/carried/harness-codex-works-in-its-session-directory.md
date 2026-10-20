---
id: harness-codex-works-in-its-session-directory
fork: kagent
theme: workspace-volumes
category: bugfix
status: carried
commits:
  - 'fix(harness): codex works in its session directory with the placeholder credentials'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3157, giantswarm/kagent-upstream#767]
  exit: 'folds into the session-directory change with #3157'
depends_on: [harness-claude-in-its-session-directory-changed, harness-agent-git-trusts-the-mirrors, harness-glab-acts-as-the-person-through-the]
verified: 2026-10-20
---
# fix(harness): codex works in its session directory with the placeholder credentials

## Change

`fix(harness): codex works in its session directory with the placeholder credentials`

## Reason

The egress gateway replaces only an `Authorization` header a request already carries. The `claude` Harness gives its git the placeholder credential helper and `GH_TOKEN`, so a workspace Session's push reaches the gateway with a Basic header the gateway swaps for the person's token. The `codex` driver ran App Server in its configured `/data/workspace` with only the person's git identity: no session directory set up, no credential helper, no mirror trust. Its `git push` to GitHub went out without `Authorization`, the gateway had nothing to replace and never asked the credential provider, GitHub answered 401 and git failed on `could not read Username`; a clone of `/mirrors/<owner>/<repo>.git` failed git's dubious-ownership check. The driver now reads the turn's dispatch like the `claude` driver: it sets the session directory up with `workspace.Prepare` (notes in `AGENTS.md`), runs App Server and the thread there, and passes `workspace.TrustMirrors` and `workspace.WithCredentialPlaceholders` in Codex's environment; the `.codex/config.toml` refusal checks the turn's directory. `TestProcessDriverWorksInTheDispatchedDirectory`, `TestProcessDriverRejectsCodexConfigInTheSessionDirectory` [#767](https://github.com/giantswarm/kagent-upstream/issues/767)

## History

to file with the session-directory change (kagent-dev/kagent#3157, branch `upstream/session-first-turn-fetch`) it extends to the codex Harness.
