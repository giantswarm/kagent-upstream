# Caller credential injection at egress

Status: design, not built. Replaces the claude harness's credential forwarder
for Harnesses that opt in.

## Problem

With `KAGENT_PROPAGATE_TOKEN=true` the caller's bearer reaches the actor: the
a2agateway adds it to the call it forwards (`go/core/internal/a2agateway/runtime.go`),
and the claude harness holds it for the turn and adds it on MCP calls through a
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
authority (`FetchSecret(uri, actor_spiffe_id)`), replacing whatever the actor
sent in that header. kagent registers a second provider, `kagent.dev`, that
answers for the caller of the current turn.

```mermaid
sequenceDiagram
    participant Caller
    participant GW as a2agateway (controller)
    participant Store as kagent.dev provider (controller)
    participant API as ate-api
    participant Actor
    participant Egress as egress gateway
    participant MCP as MCP server

    Caller->>GW: SendMessage, Authorization: Bearer B
    GW->>Store: put(actor, turn T, B, exp)
    GW->>API: rewrite the MCP host's rule: its static headers + caller from ate-secret://kagent.dev/caller/T
    GW->>Actor: SendMessage (no Authorization)
    Actor->>Egress: MCP call
    Egress->>API: GetActorEgressPolicy
    Egress->>Store: FetchSecret(ate-secret://kagent.dev/caller/T, actor)
    Egress->>MCP: MCP call, Authorization: Bearer B
    Actor-->>GW: terminal or input-required
    GW->>API: rewrite the MCP host's rule without the caller
    GW->>Store: delete(actor, T)
```

### The rule

The gateway fetches the actor's policy on every request; on the agentgateway
egress path nothing caches it (ate-api reads it from Postgres). It caches a
fetched credential for five minutes, keyed by actor and URI. Each turn gets a
fresh, unguessable `T`, so a cached bearer is never served to the next turn or
to another sender, and once the rule no longer names a caller URI nothing is
injected on new requests.

Rules match a host, and only the first matching rule's effects apply. The turn
does not add a rule: it rewrites the rule of each host that takes the caller,
keeping that rule's static injections (for example a toolset header) and adding
`Authorization` from `ate-secret://kagent.dev/caller/T`. Header names are unique
per rule and a rule holds at most 16 injections, so the translator refuses a
Harness with caller propagation whose caller host already has a static
`Authorization` binding, and it requires `https` for every caller host: the
plain HTTP egress listener injects too.

The toolset header stays a static binding in the same rule, so the gateway
overwrites it on every request and a process in the actor cannot send a wider
toolset.

### Turn boundaries

The policy, not the task run, is the record of which caller a turn uses.

- **Start.** Put the bearer in the store, rewrite the rule, then forward the
  call. If the rewrite fails, the turn fails before it is dispatched.
- **End.** On a terminal event or `input-required`, rewrite the rule without
  the caller, then delete the store entry. If the rewrite fails, delete the
  entry anyway (the rule then fails closed: the provider answers `NotFound`
  and the gateway denies the request) and retry the rewrite.
- **Other ends.** The same cleanup runs when a run ends with a stream error
  while the runtime still holds the task, on a cancel without a live run, from
  the stalled-turn sweep, and from a sweep at controller start that removes the
  caller from every instance's rules. A run that attaches with
  `SubscribeToTask` has no bearer and sets none.
- **Resume.** A resumed `input-required` turn sets the resuming caller under a
  new `T`, whoever resumes it.
- **Expiry.** A store entry also expires at the bearer's `exp`. The gateway's
  cache may still serve it for up to five minutes, so a turn longer than the
  bearer's lifetime fails on the MCP server with 401.

Each rewrite is a Get and an Update of the whole policy with the policy's
version, retried on conflict: four ate-api calls per turn, and one policy read
per egress request.

### The provider

A gRPC server in the controller implementing `credprovider.CredentialProvider`,
behind a Service in the controller's namespace. It serves with a
`servicedns.podcert.ate.dev` certificate, so the controller pod needs that
pod certificate projected and the Service has to select it; Substrate's chart
registers it as `<service>.<namespace>.svc:<port>`. It requires the egress
gateway's client certificate, chained to the `podidentity.podcert.ate.dev`
trust bundle, with the gateway's service account identity
(`spiffe://cluster.local/ns/<substrate namespace>/sa/<release>-atenet-egress`),
and refuses any other caller. It answers only for the actor whose SPIFFE ID the
gateway asserts, and `NotFound` otherwise.

The store is in memory, in the controller process that runs the a2agateway.
That holds while the controller runs one replica and is replaced, not rolled:
during a rolling update a second pod with an empty store sits behind the
Service, and its `NotFound` denies MCP calls of turns the first pod owns. Until
the store is shared, the controller Deployment uses the `Recreate` strategy.
A controller restart therefore ends MCP access for every turn in flight, where
today a running turn keeps it; long coding turns see their MCP calls denied
until the next turn sets a new caller.

### Scope

The change is opt-in per Harness and covers the claude harness. Runtimes that
read the bearer inside the actor keep doing so: the ADK's
`KAGENT_PROPAGATE_TOKEN` and STS exchange, model `APIKeyPassthrough`, and Codex.
It turns kagent-dev/kagent#2868's rule that a destination cannot combine
caller-token passthrough with gateway credentials into one header per host.

## Trust

- **Who can write policies.** ate-api authenticates its callers but does not
  authorize them per method, and `FetchSecret` carries no destination, so any
  client that may update an actor's policy can send `kagent.dev/caller/T` to
  any host the policy allows. Per-method authorization in ate-api is a
  precondition.
- **During a turn** any process in the actor can use the caller's authority on
  the hosts that take it, as with the forwarder. A request opened during the
  turn (a long MCP stream) keeps its header after the turn ends. A background
  process started in one sender's turn uses the next sender's authority, so
  instances of a Harness that runs code are not shared. Approval stays with the
  MCP server.
- **Plain HTTP.** On the plain HTTP egress listener the gateway matches the
  rule on the request's `Host` header but connects to the tunnel's CONNECT
  authority. Until those are shown to agree, credentials must not be bound to
  plain HTTP destinations; this applies to the existing static bindings too.

## What it removes

From the actor: the bearer, the forwarder and its path rules, the
clear-before-pause rule, and the second user for Claude Code. The harness's MCP
configuration points at the servers themselves.

## Prerequisites

- giantswarm/substrate#104: `credentialProvider.additionalProviders`
  registers the provider with the egress gateway.
- Per-method authorization for egress policy writes in ate-api.
- The plain HTTP listener question above answered.
