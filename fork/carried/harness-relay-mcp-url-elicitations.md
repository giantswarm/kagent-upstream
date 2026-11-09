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
  - 'fix(adk): relay a sub-agent''s URL elicitation and never show its URL to a model'
  - 'fix(adk): keep the link in a URL elicitation''s text for a client with the HITL extension'
  - 'fix(adk): refuse an unsafe URL elicitation link and give the harness the same status text'
  - 'fix(api): leave the server''s message out of a URL elicitation''s status text'
  - 'fix(api): warn of an internationalized host in a URL elicitation''s text'
  - 'fix(adk): cancel a sub-agent''s task when its input request cannot be relayed'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3499, kagent-dev/kagent#3500, giantswarm/kagent-upstream#973]
  exit: "kagent-dev/kagent#3500 (contract and Go ADK, still open) and the Claude and Codex PRs of #3499 merged; the fork-only pieces beyond #3500 (URLElicitationRequest.Nested, the exported URLElicitationText, the remote A2A tool relay and the harness cancel loop) follow kagent-dev/kagent#3499's subagents follow-up (forward a child's URL elicitation through RemoteHitlState, comment of 2026-11-03); the status text without the server's message and the internationalized-host warning go to kagent-dev/kagent#3500; drops at the re-pin that contains them. Upstream's drivers suspend by snapshot since kagent-dev/kagent 0a57368ac, so the Claude and Codex relays here are rewritten upstream, not ported"
  recheck: '2026-12-09, then each re-pin'
  owner: QuentinBisson
depends_on: []
verified: 2026-11-09
---
# feat(harness): relay MCP URL elicitations to the person as input-required

## Change

`feat(harness): park an MCP URL elicitation as auth-required`, `feat(harness): relay Claude Code's MCP URL elicitations to the person`, `feat(harness): relay Codex's MCP URL elicitations to the person`, `feat(adk): ask the person to sign in when an MCP server sends a URL elicitation`, `fix(harness): park an MCP URL elicitation as input-required, which ends the turn`, `fix(adk): relay a sub-agent's URL elicitation and never show its URL to a model`, `fix(adk): keep the link in a URL elicitation's text for a client with the HITL extension`, `fix(adk): refuse an unsafe URL elicitation link and give the harness the same status text`, `fix(api): leave the server's message out of a URL elicitation's status text`, `fix(api): warn of an internationalized host in a URL elicitation's text`, `fix(adk): cancel a sub-agent's task when its input request cannot be relayed`

## Reason

An MCP server that needs the person to open a URL, such as an MCP gateway's sign-in link for a backend the person has not connected, answers a tool call with a URL mode elicitation (`-32042` on MCP 2025-11-25, an `input_required` result on 2026-07-28) to a client that declares `elicitation.url`. Claude Code and Codex declare it, and the runtimes dropped it: the Claude driver closed stdin after the prompt and ignored `control_request` lines, the Codex driver declined every elicitation of an MCP server (and Codex fails a `-32042` call on the MCP revision it speaks by default), and the Go ADK runtime declared no elicitation at all. The link then never reached the person.

The shared runtime contract gains `URLElicitationRequest` / `URLElicitationResponse`, parked as A2A `input-required` with a `url_elicitation_request` payload of the HITL extension (`url_elicitation_response` with `accept`, `decline` or `cancel` answers it). The Claude driver keeps Claude's input open until its result and answers the stream-JSON `control_request` of subtype `elicitation`, as the Agent SDK's `onElicitation` does. The Codex adapter turns on the pinned Codex's `mcp_2026_07_28` feature, on which Codex relays URL elicitations as `mcpServer/elicitation/request` in URL mode. The Go ADK runtime builds each MCP toolset's client with an elicitation handler (go-sdk's `ClientOptions.ElicitationHandler`, through `mcptoolset.Config.Client`) and turns a URL elicitation during a tool call into an ADK tool confirmation that carries it; google/adk-go#1168 would move the handler into `mcptoolset.Config`. A form mode elicitation is declined everywhere, since what the person typed would reach the model's context.

## History

Proven against the pinned Claude Code 2.1.303 and Codex 0.164.0 with `KAGENT_CLAUDE_BINARY` and `KAGENT_CODEX_BINARY` integration tests (a fake model, an MCP server answering on both revisions).

The first release parked the request as `auth-required`. a2a-go ends a streamed execution only on a terminal state or `input-required`, so the stream stayed open until the client's deadline; agentlab's sign-in proof showed it on the Go ADK runtime. The request is now parked as `input-required`, the state of the extension's other requests.

The Go ADK runtime put the URL in the status text of an A2A caller that had not activated the HITL extension, where a calling agent's model read it; named the server by the name it reported about itself, so a server could pose as another; and failed a sub-agent's URL elicitation in the parent instead of relaying it (giantswarm/kagent-upstream#973). The sign-in tool now fails the call for such a caller without the URL, the Go ADK's remote A2A tool relays a child's request as a `url_elicitation_request` with `nested` correlation (and cancels the child's task when its own caller cannot be shown the URL, or when the child's request cannot be relayed), the server is named by its configured entry (`name` on the ADK's MCP server configuration: the RemoteMCPServer's name or the `mcp.json` key), and no runtime's status text carries the URL or the server's message.

The status text keeps the URL for a client that activated the HITL extension, as kagent-dev/kagent#3500 does: the kagent UI activates it and renders only the text of a URL elicitation, so the link is its only way to it. Unlike #3500, the text leaves out the server's `message` (MCP 2026-07-28: a client should not render a URL as clickable outside `url`, and the kagent UI renders the text as Markdown with autolinks) and warns when the URL's host is an internationalized name, naming its ASCII form; the URL acceptance is unchanged, byte for byte the same as #3500's.

A runtime refuses a URL that is not https, or http on a loopback host, or that carries user info, as kagent-dev/kagent#3500 does with `ValidateElicitationURL`: an MCP server's elicitation with such a URL is declined, and a child agent's is not relayed. The harness runtime composes the status text the Go ADK's way, from the shared `URLElicitationText`, and answers a URL elicitation with `cancel` for a caller that did not activate the HITL extension instead of sending it the URL.

The fork-only pieces beyond kagent-dev/kagent#3500 (still open) are `URLElicitationRequest.Nested` and the nested resume, the exported `URLElicitationText` that the harness shares, the remote A2A tool relay of a child's URL elicitation with its cancel paths, and the harness loop that answers `cancel` for a caller without the HITL extension. The Python ADK's remote A2A tool does not relay a child's URL elicitation and does not cancel the child's task: it fails the call as a request without a valid HITL payload, a known gap of the Python parent.
