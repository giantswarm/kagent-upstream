---
id: harness-relay-mcp-url-elicitations
fork: kagent
theme: harness-variety
category: feature
status: carried
commits:
  - 'feat(harness): park an MCP URL elicitation as auth-required'
  - 'feat(harness): relay Claude Code''s MCP URL elicitations to the person'
  - 'feat(harness): relay Codex''s MCP URL elicitations to the person'
  - 'feat(adk): ask the person to sign in when an MCP server sends a URL elicitation'
  - 'fix(harness): park an MCP URL elicitation as input-required, which ends the turn'
upstream:
  outcome: to-file
  refs: []
  exit: a kagent-dev/kagent pull request carrying the five commits, filed once the line's proof is in; drops at the re-pin that contains it
depends_on: []
verified: 2026-10-26
---
# feat(harness): relay MCP URL elicitations to the person as input-required

## Change

`feat(harness): park an MCP URL elicitation as auth-required`, `feat(harness): relay Claude Code's MCP URL elicitations to the person`, `feat(harness): relay Codex's MCP URL elicitations to the person`, `feat(adk): ask the person to sign in when an MCP server sends a URL elicitation`, `fix(harness): park an MCP URL elicitation as input-required, which ends the turn`

## Reason

An MCP server that needs the person to open a URL, such as an MCP gateway's sign-in link for a backend the person has not connected, answers a tool call with a URL mode elicitation (`-32042` on MCP 2025-11-25, an `input_required` result on 2026-07-28) to a client that declares `elicitation.url`. Claude Code and Codex declare it, and the runtimes dropped it: the Claude driver closed stdin after the prompt and ignored `control_request` lines, the Codex driver declined every elicitation of an MCP server (and Codex fails a `-32042` call on the MCP revision it speaks by default), and the Go ADK runtime declared no elicitation at all. The link then never reached the person.

The shared runtime contract gains `URLElicitationRequest` / `URLElicitationResponse`, parked as A2A `input-required` with a `url_elicitation_request` payload of the HITL extension (`url_elicitation_response` with `accept`, `decline` or `cancel` answers it). The Claude driver keeps Claude's input open until its result and answers the stream-JSON `control_request` of subtype `elicitation`, as the Agent SDK's `onElicitation` does. The Codex adapter turns on the pinned Codex's `mcp_2026_07_28` feature, on which Codex relays URL elicitations as `mcpServer/elicitation/request` in URL mode. The Go ADK runtime builds each MCP toolset's client with an elicitation handler (go-sdk's `ClientOptions.ElicitationHandler`, through `mcptoolset.Config.Client`) and turns a URL elicitation during a tool call into an ADK tool confirmation that carries it; google/adk-go#1168 would move the handler into `mcptoolset.Config`. A form mode elicitation is declined everywhere, since what the person typed would reach the model's context.

## History

Proven against the pinned Claude Code 2.1.303 and Codex 0.164.0 with `KAGENT_CLAUDE_BINARY` and `KAGENT_CODEX_BINARY` integration tests (a fake model, an MCP server answering on both revisions).

The first release parked the request as `auth-required`. a2a-go ends a streamed execution only on a terminal state or `input-required`, so the stream stayed open until the client's deadline; agentlab's sign-in proof showed it on the Go ADK runtime. The request is now parked as `input-required`, the state of the extension's other requests.
