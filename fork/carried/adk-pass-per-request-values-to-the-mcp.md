---
id: adk-pass-per-request-values-to-the-mcp
fork: kagent
theme: mcp-forwarder
category: feature
status: carried
commits:
  - 'feat(adk): pass per-request values to the MCP servers that declare their extension'
  - 'fix(adk): negotiate request-scoped tool-call metadata'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3807, kagent-dev/kagent#3791]
  exit: '#3791 merges; the first re-pin onto a commit containing it drops this commit'
depends_on: []
verified: 2026-11-26
---
# Request-scoped MCP tool-call metadata

## Change

The Go ADK forwards caller-supplied, request-scoped tool-call metadata through a versioned A2A extension and an explicitly negotiated MCP extension.

## Reason

An application can supply values such as its own session ID without putting them in the prompt. A client activates `https://kagent.dev/extensions/mcp-tool-call-metadata/v1` and places qualified metadata keys and opaque JSON values under that URI in the request metadata. The runtime advertises `dev.kagent/tool-call-metadata` on its MCP clients and forwards only keys each server accepts through that extension's `keys` settings. The filtered object is nested under `_meta["dev.kagent/tool-call-metadata"]` on `tools/call`, on MCP 2025-11-25 and 2026-07-28. Malformed activated payloads are refused before execution. The contract is in `docs/architecture/mcp-tool-call-metadata-extension.md`.

The implementation follows kagent-dev/kagent#3791, with adaptations for this line's older pin:

- `extensionRequested` and `ensureExtension`, which upstream supplies with its usage extension, are defined in `go/adk/pkg/a2a/extensions.go`. HITL uses the same comma-separated activation parser.
- The fixed MCP capability and sending middleware are added to `newElicitingClient` (`go/adk/pkg/mcp/signin.go`), instead of upstream's `newMCPAppClient`.
- The card and the interceptors list only HITL and this extension, without upstream's usage extension.
- This line requires explicit runtime image digests and has no upstream release image catalog in the compiler. The chart supplies its release Go ADK digest through the internal `KAGENT_GO_ADK_RELEASE_IMAGE` setting. Public discovery declares the capability only for that exact image without command or argument overrides; custom runtimes receive no inferred capability. The newer upstream image catalog replaces this wiring on re-pin.
- The older ADK toolset has no close method; protocol-test fixtures close the HTTP server's client connections.

The extension contract is proposed through kagent-dev/kagent#3807 and implemented by kagent-dev/kagent#3791. These identifiers describe project-owned extensions, not arbitrary MCP extension support or an OAuth mechanism.

## History

2026-11-26: carried, cherry-picked from kagent-dev/kagent#3791 (`87db9fef6`).

2026-11-26: aligned the negotiated metadata contract, naming, public discovery, and protocol tests with the upstream review.
