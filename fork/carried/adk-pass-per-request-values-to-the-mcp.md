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
  - 'refactor(adk): forward ordinary A2A request metadata'
  - 'fix(adk): refuse request metadata under prefixes MCP reserves'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3807, kagent-dev/kagent#3791]
  exit: '#3791 merges with the reserved second-label prefix guard and pre-dispatch rejection tests; the first re-pin containing that behavior drops this commit'
depends_on: []
verified: 2026-11-27
---
# Request-scoped MCP metadata

## Change

The Go ADK forwards ordinary A2A request-level metadata directly into MCP tool-call `_meta`, without custom A2A or MCP extensions, activation or capability declarations.

## Reason

Applications can supply request-scoped values outside model prompts and tool arguments. Every called server can receive the complete application metadata. Top-level names follow MCP syntax, transport-owned fields are protected, and absent follow-up values inherit nothing. See `docs/architecture/mcp-request-metadata.md`.

The implementation follows kagent-dev/kagent#3791, adapted to this line's older pin. Sending middleware is installed in `newElicitingClient` rather than `newMCPAppClient`. This line also supplies the shared comma-separated HITL activation parser, which is independent of metadata forwarding. Its older ADK toolset has no close method, so protocol fixtures close HTTP client connections.

The upstream exit is kagent-dev/kagent#3791 and kagent-dev/kagent#3807. Forwarding values neither implements application semantics nor supplies authorization.

## History

2026-11-26: carried, cherry-picked from kagent-dev/kagent#3791 (`87db9fef6`).

2026-11-26: aligned the negotiated metadata contract, naming, public discovery, and protocol tests with the upstream review.

2026-11-26: carried kagent-dev/kagent#3791's contract text: values are not credentials, only `tools/call` carries them, and the key grammar is the contract's own rule.

2026-11-26: distinguished HTTP bearer headers from stdio environment credentials in the authorization contract.

2026-11-26: aligned with the opaque payload contract prepared for kagent-dev/kagent#3791; extension support replaces accepted-key settings and nested-key validation.

2026-11-26: replaced the dedicated MCP carrier and capability with generic request-scoped `_meta` forwarding through a new A2A bridge URI.

2026-11-26: removed the custom A2A bridge, activation and capability advertisement; ordinary request metadata is forwarded directly.

2026-11-27: keys under a prefix whose second label is `modelcontextprotocol` or `mcp`, in any case, are refused, as muster and klaus-gateway refuse them.
