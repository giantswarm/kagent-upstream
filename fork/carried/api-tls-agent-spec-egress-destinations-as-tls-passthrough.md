---
id: api-tls-agent-spec-egress-destinations-as-tls-passthrough
fork: kagent
theme: egress-policies
category: feature
status: dropped
commits:
  - 'feat(api): tls:// Agent.spec.egress destinations as TLS passthrough'
upstream:
  outcome: fork-only
  refs: [giantswarm/kagent-upstream#740]
  exit: retires with Agent.spec.egress, once consumers render an EgressPolicy per agent
depends_on: [api-agent-spec-egress-http-s-origins, api-tls-egresspolicy-destinations-as-tls-passthrough]
verified: 2026-11-26
---
# feat(api): tls:// Agent.spec.egress destinations as TLS passthrough

## Change

`feat(api): tls:// Agent.spec.egress destinations as TLS passthrough`

## Reason

an Agent that needs a TLS service that is not HTTP, such as a database or a message broker, could not declare it on the Agent: `Agent.spec.egress` accepted HTTP(S) origins only, and an `EgressPolicy`, which takes `tls://` destinations, is written by a tenant admin, not by whoever edits the Agent (the Generic agent chart, an editor through the platform's portal). `Agent.spec.egress` now also accepts `tls://<host>:<port>` with the grammar of an `EgressPolicy` `tls://` destination (lowercase DNS name, a leftmost-label wildcard over at least two labels, no IP address, a required port, no path): the CRD item pattern and CEL rule, `egress.ParseOrigin` and `substrate.ActorEgressPolicy` share it, and each entry compiles to one `TLSPassthroughRule`. The compiler refuses, as a validation error on the Agent, an entry whose hostname pattern and port another destination of the revision reaches over a different protocol. Persisted state keeps its format: a revision's destinations are origin strings, and a `tls://` entry is one more of them. `TestCompileAgentAddsTheAgentEgress`, `TestAgentEgressTLSConflictsWithEgressPolicy`, the `TLS` cases of the Agent egress CEL tests

## History

fork-only: upstream declined `Agent.spec.egress` (kagent-dev/kagent#3019) for `EgressPolicy` (kagent-dev/kagent#3177); this change extends the carried field and leaves with it.

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), retired: with Agent.spec.egress retirement; tls:// carried on EgressPolicy row; each Agent.spec.egress list becomes an EgressPolicy <agent>-egress by the kagent-crds conversion hooks (crds-convert-the-lines-1-x-api-kagent-dev). The next landed re-pin removes this file
