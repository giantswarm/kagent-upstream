---
id: harness-glab-acts-as-the-person-through-the
fork: kagent
theme: credential-injection
category: feature
status: dropped
commits:
  - 'feat(harness): glab acts as the person through the placeholder like gh'
  - 'fix(harness): glab gets its placeholder in a workspace only'
  - 'refactor(harness): move CLI setup to platform configuration'
upstream:
  outcome: drops-at-repin
  refs: []
  exit: 'the next re-pin folds the CLI helpers and their removal out; deploy platform Harness literals before consuming the removal release'
depends_on: [adk-run-bash-tool-commands-as-the-person, giantswarm/substrate:egress-scopes-credential-header-to-request-path-prefix]
verified: 2026-11-26
---
# feat(harness): glab acts as the person through the placeholder like gh

## Change

`feat(harness): glab acts as the person through the placeholder like gh` and `fix(harness): glab gets its placeholder in a workspace only`

## Reason

in a Session of a GitLab workspace `git push` reached GitLab as the person, but `glab` could not: without a token glab sends no credential, and with one from `GITLAB_TOKEN` it sends a `PRIVATE-TOKEN` header, neither of which the egress gateway replaces, so `glab api user` answered 401 and the Session could not open a merge request (giantswarm/kagent-upstream#723). `WithCredentialPlaceholders` now also sets `GITLAB_TOKEN` to the placeholder and `GLAB_IS_OAUTH2=true` in a turn of a workspace (a dispatch with a mirrors directory; a Session without one gets no glab placeholder), so glab sends `Authorization: Bearer <placeholder>`, which the gateway replaces with the person's token on a GitLab host; a glab token the environment sets (`GITLAB_TOKEN`, `GITLAB_ACCESS_TOKEN`, `OAUTH_TOKEN`) stands, and the bash tool's allowlist keeps a runtime's real token from the command. glab also acts only on a remote whose host its configuration knows besides gitlab.com, so the workspace setup writes `.kagent/glab-cli/config.yml` (0600) naming the host of every http(s) origin but github.com as an OAuth2 GitLab host, and `GLAB_CONFIG_DIR` points at it. `TestWithCredentialPlaceholders`, `TestGlabConfig`, `TestPrepareWritesGlabConfig`, `TestExecuteCommand_TurnEnvironment`

## History

The CLI helper and generated glab configuration are removed. The platform owns
the literal token, OAuth mode, HTTPS git protocol and host through Harness.spec.env. A single
configured GitLab provider supplies its host; multiple GitLab hosts require an
explicit choice on every Harness. No git remote is classified as a forge.
Declared values pass actual git, gh and glab protocol checks, including direct
and subprocess workspace setup. Session grants and gateway bindings remain
independent; rollout follows the platform declarations and real Session proof.

to file with `feat(adk): run bash tool commands as the person of the turn`, whose placeholder it extends

2026-11-20: class 3 platform for glab configuration; generic upstream candidate for preserving explicitly declared shell values. Upstream's environment policy rejects secret injection and SDK-specific placeholder generation, not safe literal Harness values. Harness.spec.env already supports literals; no per-Session environment API is required. Use GLAB_IS_OAUTH2 and an explicit GITLAB_HOST or platform-owned host configuration, including self-hosted origins. A deadline passing is not grounds for removing path-scoped gateway bindings. The helper remains until replacement checks pass. OMP's dispatch/setup and toolchain PATH gaps have local replacements and subprocess tests; real Session validation remains required.

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), retired: none needed: already dropped; platform Harness literals replace. The next landed re-pin removes this file
