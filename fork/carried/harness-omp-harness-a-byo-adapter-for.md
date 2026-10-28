---
id: harness-omp-harness-a-byo-adapter-for
fork: kagent
theme: harness-variety
category: gs-specific
status: carried
commits:
  - 'feat(harness): omp-harness, a BYO adapter for oh-my-pi'
  - 'fix(harness/omp): report the turn''s cost through the optional usage cost'
  - 'feat(harness/omp): load the skills an Agent selects from Agent Plugins'
  - 'feat(harness/omp): run Shared subagent bindings as omp task agents'
  - 'feat(harness/omp): relay MCP URL elicitations through the MCP bridge'
upstream:
  outcome: fork-only
  refs: [can1357/oh-my-pi#8808]
  exit: move out of tree as a BYO image in agent-platform
depends_on: []
verified: 2026-10-27
---
# feat(harness): omp-harness, a BYO adapter for oh-my-pi

## Change

`feat(harness): omp-harness, a BYO adapter for oh-my-pi` and `fix(harness/omp): report the turn's cost through the optional usage cost`

## Reason

the platform offers oh-my-pi (omp) next to claude and codex, and kagent has no omp runtime; `go/harness/omp` is a BYO Harness image on kagent's public runtime packages (the private A2A executor, the continuation, `/readyz`, the TaskStore) that drives `omp --mode rpc` per turn, takes claude's Anthropic-protocol ModelConfig, reports each turn's usage, resumes from omp's session under `/data`, serves a binding's selected MCP tools through a loopback bridge, and pins omp's isolation so a repository's omp, `.claude` or `.codex` configuration adds no tool, server or provider

`feat(harness/omp): relay MCP URL elicitations through the MCP bridge`: omp's MCP client declares no elicitation, so an MCP gateway's sign-in link reached omp, and the model, as text. Every server is now reached through the loopback bridge, a go-sdk MCP client that declares `elicitation.url`, serves omp the server's tools, resources, resource templates and prompts and follows their list changes. A URL elicitation during a tool call parks the turn as `input-required` with a `url_elicitation_request` (the shared runtime contract of `harness-relay-mcp-url-elicitations`) while the bridge holds omp's call open and the driver keeps omp running; the person's answer releases it. The binding's timeout stops while the person decides and omp's own MCP timeout is off. The bridge's elicitation role ends once oh-my-pi relays URL elicitations itself (can1357/oh-my-pi#8808; #8809 is open, interactive mode only); the driver then relays omp's own request.

## History

fork-only: a BYO adapter of the platform's, a pure addition under `go/harness/omp` that a re-pin never conflicts with; an upstream omp runtime would be an API change the platform does not ask for
