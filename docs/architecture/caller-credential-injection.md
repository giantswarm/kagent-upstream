# Caller credential injection at egress

Status: design, not built. Replaces the claude harness's credential forwarder.

## Problem

With `KAGENT_PROPAGATE_TOKEN=true` the caller's bearer reaches the actor: the
a2agateway adds it to the call it forwards (`go/core/internal/a2agateway/runtime.go`),
and the harness holds it for the turn and adds it on MCP calls through a
loopback forwarder. The actor runs code nobody vetted (a cloned repository's
tests, an npm postinstall script), so keeping the bearer away from that code
depends on a boundary inside the sandbox: a second user for Claude Code, and
gVisor enforcing it. The bearer is also in the actor's memory when the actor
pauses, so the forwarder has to clear it before every pause.

## Design

The bearer never enters the actor. Substrate's egress gateway already injects
credentials into an actor's outgoing requests: a rule in the actor's
`EgressPolicy` names a header, a prefix and an `ate-secret://<authority>/...`
URI, and the gateway resolves the URI through the provider registered for that
authority (`FetchSecret(uri, actor_spiffe_id)`), overwriting whatever the actor
sent. kagent registers a second provider, `kagent.dev`, that answers for the
caller of the current turn.

```mermaid
sequenceDiagram
    participant Caller
    participant GW as a2agateway (controller)
    participant Store as kagent.dev provider (controller)
    participant API as ate-api
    participant Actor
    participant Egress as egress gateway
    participant Muster

    Caller->>GW: SendMessage, Authorization: Bearer B
    GW->>Store: put(actor, turn T, B, exp)
    GW->>API: UpdateActorEgressPolicy(rule on muster: Authorization from ate-secret://kagent.dev/caller/T)
    GW->>Actor: SendMessage (no Authorization)
    Actor->>Egress: MCP call to muster
    Egress->>API: GetActorEgressPolicy
    Egress->>Store: FetchSecret(ate-secret://kagent.dev/caller/T, actor)
    Egress->>Muster: MCP call, Authorization: Bearer B
    Actor-->>GW: terminal or input-required
    GW->>API: UpdateActorEgressPolicy(caller rule removed)
    GW->>Store: delete(actor, T)
```

- **Per-turn URI.** The gateway caches a fetched credential for five minutes
  per actor and URI, and fetches the policy on every request. Each turn gets a
  fresh `T`, so a cached bearer is never served to the next turn or to another
  sender, and once the rule is removed nothing is injected at all.
- **Static parts of the rule.** The translator compiles, for a Harness with
  caller propagation, the hosts that take the caller (each RemoteMCPServer host
  and the git routes) and muster's toolset header. The toolset header is served
  by the same provider (`ate-secret://kagent.dev/toolset/<server>`), so the
  gateway overwrites it and a process in the actor cannot choose a wider
  toolset.
- **Turn boundaries.** The task run already owns a task from `dispatch` to its
  terminal event or `input-required`. The rule is set before the call is
  forwarded and removed when the run ends or parks; a resumed call sets the
  resuming caller's bearer under a new `T`. A store entry also expires at the
  bearer's `exp`.
- **Provider.** A gRPC server in the controller implementing
  `credprovider.CredentialProvider`, behind a Service. It requires the egress
  gateway's client certificate (`atenet-egress` service account identity) and
  serves with a `servicedns.podcert.ate.dev` certificate, so the gateway
  verifies it with the trust bundle it already has. It answers only for the
  actor whose SPIFFE ID the gateway asserts, and `NotFound` otherwise, which
  the gateway turns into a denied request.
- **Store.** In memory, in the controller process that runs the a2agateway.
  The controller runs one replica today. A restart loses every entry, which
  ends the turns in flight the same way a restart already does; the next turn
  sets a new one. More replicas need a shared store; not in scope.
- **Policy reconcile.** `EnsureActorEgressPolicy` compares the whole policy
  with the prepared revision. It has to compare the revision's rules only and
  leave the caller rule to the task run, or a restart during a turn reports a
  mismatch.
- **Substrate.** No code change: agentgateway already selects a provider by URI
  authority. The chart registers the provider with
  `credentialProvider.additionalProviders` (giantswarm/substrate#104).

## What it removes and what it keeps

Removed from the actor: the bearer, the forwarder and its path rules, the
clear-before-pause rule, and the reason for running Claude Code as a second
user. The harness's MCP configuration points at the servers themselves.

Kept: during a turn any process in the actor can use the caller's authority on
the hosts the rule names, as with the forwarder. Approval stays with muster.

## Constraints

- Injection happens only on TLS connections the gateway terminates. muster and
  the git routes must be reached over HTTPS, or the request leaves without the
  caller.
- Rules match a host. Two RemoteMCPServers on one muster host with different
  toolsets cannot be told apart.
- Two policy writes per turn through ate-api.
