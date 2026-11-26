---
id: adk-pass-per-request-values-to-the-mcp
fork: kagent
theme: mcp-forwarder
category: feature
status: carried
commits:
  - 'feat(adk): pass per-request values to the MCP servers that declare their extension'
  - 'fix(adk): negotiate request-scoped tool-call metadata'
  - 'docs(adk): state that tool-call metadata values are not credentials'
  - 'docs(adk): clarify authorization by transport'
  - 'fix(adk): forward opaque tool-call metadata to participating servers'
  - 'refactor(adk): forward request metadata directly to MCP tool calls'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3807, kagent-dev/kagent#3791]
  exit: '#3791 merges; the first re-pin onto a commit containing it drops this commit'
depends_on: []
verified: 2026-11-26
---
# Request-scoped MCP metadata

## Change

The Go ADK forwards caller-supplied, request-scoped metadata through a versioned A2A bridge directly into MCP tool-call `_meta`.

## Reason

An application can supply values such as its own session ID without putting them in the prompt. A client activates `https://kagent.dev/extensions/mcp-request-metadata/v1` and supplies an object under that URI. Its entries become `tools/call._meta` directly, without a kagent MCP carrier or capability declaration, on MCP 2025-11-25 and 2026-07-28. Every called server can receive the complete metadata; there are no application-key policies or destination declaration checks. Top-level names follow MCP's key syntax, nested values remain opaque, and client-owned protocol fields are protected. Malformed activated envelopes are refused before execution. The contract is in `docs/architecture/mcp-request-metadata-extension.md`.

The implementation follows kagent-dev/kagent#3791, with adaptations for this line's older pin:

- `extensionRequested` and `ensureExtension`, which upstream supplies with its usage extension, are defined in `go/adk/pkg/a2a/extensions.go`. HITL uses the same comma-separated activation parser.
- The sending middleware is added to `newElicitingClient` (`go/adk/pkg/mcp/signin.go`), instead of upstream's `newMCPAppClient`.
- The card and the interceptors list only HITL and this extension, without upstream's usage extension.
- This line requires explicit runtime image digests and has no upstream release image catalog in the compiler. The chart supplies its release Go ADK digest through the internal `KAGENT_GO_ADK_RELEASE_IMAGE` setting. Public discovery declares the capability only for that exact image without command or argument overrides; custom runtimes receive no inferred capability. The newer upstream image catalog replaces this wiring on re-pin.
- The older ADK toolset has no close method; protocol-test fixtures close the HTTP server's client connections.

The bridge contract is proposed through kagent-dev/kagent#3807 and implemented by kagent-dev/kagent#3791. Transporting metadata does not implement its application semantics, negotiate arbitrary extensions or provide authorization.

## History

2026-11-26: carried, cherry-picked from kagent-dev/kagent#3791 (`87db9fef6`).

2026-11-26: aligned the negotiated metadata contract, naming, public discovery, and protocol tests with the upstream review.

2026-11-26: carried kagent-dev/kagent#3791's contract text: values are not credentials, only `tools/call` carries them, and the key grammar is the contract's own rule.

2026-11-26: distinguished HTTP bearer headers from stdio environment credentials in the authorization contract.

2026-11-26: aligned with the opaque payload contract prepared for kagent-dev/kagent#3791; extension support replaces accepted-key settings and nested-key validation.

2026-11-26: replaced the dedicated MCP carrier and capability with generic request-scoped `_meta` forwarding through a new A2A bridge URI.
