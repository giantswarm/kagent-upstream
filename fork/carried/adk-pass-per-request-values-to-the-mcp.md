---
id: adk-pass-per-request-values-to-the-mcp
fork: kagent
theme: mcp-forwarder
category: feature
status: carried
commits:
  - 'feat(adk): pass per-request values to the MCP servers that declare their extension'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3807, kagent-dev/kagent#3791]
  exit: '#3791 merges; the first re-pin onto a commit containing it drops this commit'
depends_on: []
verified: 2026-11-26
---
# feat(adk): pass per-request values to the MCP servers that declare their extension

## Change

`feat(adk): pass per-request values to the MCP servers that declare their extension`

## Reason

An application that puts an agent in front of its own users needs its MCP tools to get a per-request value, such as its own session id, without the value going through the prompt, where the model sees it and can change it. The Go ADK runtime gets the versioned A2A extension `https://kagent.dev/extensions/mcp-extensions/v1`: a client activates it and sends values keyed by MCP extension id in the request metadata under the extension URI. The runtime sends each value as `tools/call` `_meta` only to MCP servers that declared that extension in `capabilities.extensions`, on MCP 2025-11-25 and 2026-07-28, and refuses malformed values with invalid params before anything runs. The contract is in `docs/architecture/mcp-extensions-extension.md`.

The commit is kagent-dev/kagent#3791's, with three differences forced by the line's older pin:

- `extensionRequested` and `ensureExtension`, which upstream added with the token usage extension the line does not carry, are defined in `go/adk/pkg/a2a/mcp_extensions.go`.
- The sending middleware is added to `newElicitingClient` (`go/adk/pkg/mcp/signin.go`), the line's MCP client for tool calls, instead of upstream's `newMCPAppClient`.
- The card and the interceptors list only HITL and this extension, without upstream's usage extension.

The extension URI is proposed in kagent-dev/kagent#3807; if upstream names it differently, the A2A clients that send it accept both URIs for one release.

## History

2026-11-26: carried, cherry-picked from kagent-dev/kagent#3791 (`87db9fef6`).
