---
id: claude-accept-a-claude-code-oauth-token-as
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(claude): accept a Claude Code OAuth token as the Anthropic credential'
upstream:
  outcome: to-file
  refs: []
  exit: "prepared in the fork: branch upstream/claude-oauth-token (mirror main @ 92dd6caa + the patch, DCO-signed, no fork references); upstream search 2026-10-14 for OAuth token, CLAUDE_CODE_OAUTH_TOKEN, setup-token: nothing"
depends_on: []
verified: 2026-10-24
---
# feat(claude): accept a Claude Code OAuth token as the Anthropic credential

## Change

`feat(claude): accept a Claude Code OAuth token as the Anthropic credential`

## Reason

a Claude agent could only run on an Anthropic API key: the Claude compiler always set `ANTHROPIC_API_KEY`, which Claude Code ranks above its own OAuth token, and the gateway only knew `x-api-key` for Anthropic. A ModelConfig whose `anthropic.credentialType` is `oauthToken` now sets `CLAUDE_CODE_OAUTH_TOKEN` and binds the token as `authorization: Bearer` on the Anthropic host; the ADK runtimes reject the mode, Anthropic accepts the token from Claude Code alone

## History

carried from giantswarm/kagent-upstream#595 (agentlab proof green 2026-10-15)
