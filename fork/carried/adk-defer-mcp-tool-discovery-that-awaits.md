---
id: adk-defer-mcp-tool-discovery-that-awaits
fork: kagent
theme: mcp-forwarder
category: bugfix
status: dropped
commits:
  - 'fix(adk): defer MCP tool discovery that awaits the caller''s credential'
upstream:
  outcome: to-file
  refs: []
  exit: file the branch
depends_on: []
verified: 2026-11-26
---
# fix(adk): defer MCP tool discovery that awaits the caller's credential

## Change

`fix(adk): defer MCP tool discovery that awaits the caller's credential`

## Reason

an agent's golden boot runs the Go ADK to readiness with no person behind it, and the ADK listed the tools of every MCP server at startup to classify its MCP App tools. A server that takes the caller's token (`KAGENT_PROPAGATE_TOKEN`, the per-agent muster `RemoteMCPServer`) answered `401` to that unauthenticated list, and the egress access log of every golden boot read like an authentication outage. The ADK now leaves such a server alone at startup when the boot has no `Authorization` for it, logs the deferral at debug, and discovers the tools at the first turn with the caller's token, as the 401 fallback already did; a server with a static credential is still classified at startup (giantswarm/kagent-upstream#99)

## History

to file

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), upstream provides it: kagent-dev/kagent#3560 ccb2dd350 via backport a08cde76a (#3586): all HTTP/SSE/stdio MCP connections deferred to invocation, go/adk/pkg/mcp/discovery.go:21-36; docs/architecture/mcp.md. The next landed re-pin removes this file
