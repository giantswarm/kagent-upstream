---
id: harness-glab-acts-as-the-person-through-the
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(harness): glab acts as the person through the placeholder like gh'
  - 'fix(harness): glab gets its placeholder in a workspace only'
upstream:
  outcome: fork-only
  refs: []
  exit: "moves to agent-manager (the glab environment on a Session with providers; the config file from the workspace setup): drops at that re-pin; loses its function and drops with the path_prefix family on 2026-11-17 if agent-substrate/substrate#2541 stays unanswered"
depends_on: [adk-run-bash-tool-commands-as-the-person, giantswarm/substrate:egress-scopes-credential-header-to-request-path-prefix]
verified: 2026-10-20
---
# feat(harness): glab acts as the person through the placeholder like gh

## Change

`feat(harness): glab acts as the person through the placeholder like gh` and `fix(harness): glab gets its placeholder in a workspace only`

## Reason

in a Session of a GitLab workspace `git push` reached GitLab as the person, but `glab` could not: without a token glab sends no credential, and with one from `GITLAB_TOKEN` it sends a `PRIVATE-TOKEN` header, neither of which the egress gateway replaces, so `glab api user` answered 401 and the Session could not open a merge request (giantswarm/kagent-upstream#723). `WithCredentialPlaceholders` now also sets `GITLAB_TOKEN` to the placeholder and `GLAB_IS_OAUTH2=true` in a turn of a workspace (a dispatch with a mirrors directory; a Session without one gets no glab placeholder), so glab sends `Authorization: Bearer <placeholder>`, which the gateway replaces with the person's token on a GitLab host; a glab token the environment sets (`GITLAB_TOKEN`, `GITLAB_ACCESS_TOKEN`, `OAUTH_TOKEN`) stands, and the bash tool's allowlist keeps a runtime's real token from the command. glab also acts only on a remote whose host its configuration knows besides gitlab.com, so the workspace setup writes `.kagent/glab-cli/config.yml` (0600) naming the host of every http(s) origin but github.com as an OAuth2 GitLab host, and `GLAB_CONFIG_DIR` points at it. `TestWithCredentialPlaceholders`, `TestGlabConfig`, `TestPrepareWritesGlabConfig`, `TestExecuteCommand_TurnEnvironment`

## History

to file with `feat(adk): run bash tool commands as the person of the turn`, whose placeholder it extends

2026-10-20, decided on internal issue 38652, comment 6301541135: class 3 platform; destination agent-manager renders `GITLAB_TOKEN`, `GLAB_IS_OAUTH2` and `GLAB_CONFIG_DIR` on a Session with providers, the config file comes from the workspace setup (theme T2); upstream declines to configure tools' environment by policy (`docs/architecture/egress-policies.md:118-127`); first step a new branch in agent-manager; test `TestWithCredentialPlaceholders` and `TestGlabConfig`; depends on theme T2 and on the path_prefix family (Bearer under the API path of the git host); owner unassigned.
