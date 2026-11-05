---
id: models-vertex-ai-through-a-gateway-minted-google
fork: kagent
theme: providers-vertex
category: feature
status: carried
commits:
  - 'feat(models): Vertex AI through a gateway-minted Google access token'
  - 'fix(models): Vertex AI calls send the Authorization the gateway replaces'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#2888, kagent-dev/kagent#3258, kagent-dev/kagent#3259, kagent-dev/substrate#47, kagent-dev/kagent#3359, agent-substrate/substrate#3349, agent-substrate/substrate#2911]
  exit: '#3259 merges (modelCredentialAuthorities); plugin class off *.k8s.io'
depends_on: [kagent-dev/substrate#90]
verified: 2026-11-02
---
# feat(models): Vertex AI through a gateway-minted Google access token

## Change

`feat(models): Vertex AI through a gateway-minted Google access token` (with `fix(models): Vertex AI calls send the Authorization the gateway replaces`)

## Reason

a Vertex ModelConfig (`AnthropicVertexAI`, `GeminiVertexAI`) with `apiKeySecret` rendered the key as a mounted file, which the Substrate actor runtime cannot mount, so every AgentTemplate on it stayed `Compatible=False UnsupportedConfiguration` (giantswarm/kagent-upstream#178); the shared credential compiler binds `authorization: Bearer` on the location's Vertex hostname to `ate-secret://google-access-token.k8s.io/default/<ns>/<secret>/<key>`, which the Google access-token credential provider plugin (giantswarm/substrate-credential-providers; the Substrate line's bundled provider until 1.12, giantswarm/substrate#250) answers with a `cloud-platform` access token minted from the key; the Go ADK runtime (`KAGENT_SKIP_VERTEX_AUTH`) and Claude Code (`CLAUDE_CODE_SKIP_VERTEX_AUTH`) call Vertex AI without Google credentials of their own and send the placeholder bearer the gateway replaces (it adds no header a request lacks), the compilers check the key (service account, project, Google token endpoint), and the key never reaches a runtime

## History

kagent-dev/kagent#2888, which covered the Claude harness half on kagent-dev/substrate#47's `google-access-token.kubernetes.io` grammar, was closed unmerged by the stale bot on 2026-10-12 (kagent-dev/substrate#47 closed unreviewed the same day); the exit is kagent-dev/kagent#3259 (draft, 2026-10-18), which covers the Go ADK, Python ADK and Claude halves through a configurable credential authority

2026-11-02: kagent-dev/kagent#3259 is open and unreviewed, rebased on kagent-dev/kagent#3359, whose docs still call Vertex service-account and Vertex Actor JWT credentials unsupported. Asked the maintainer on #3259 to choose the token source: the configurable credential authority, or the RFC 8693 exchange of the Actor JWT at the Substrate egress (agent-substrate/substrate#3349, Workload Identity Federation docs in agent-substrate/substrate#2911; only the atenet dataplane exchanges, not agentgateway). The runtime half of #3259 is needed either way.
