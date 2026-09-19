# Runtime and Lifecycle

An `AgentInstance` is PostgreSQL-backed control-plane state exposed through gRPC.
It pins one prepared revision and names one Substrate Actor. It is not a
Kubernetes resource.

## Creation and state

Creation selects the latest successful revision for the Harness/AgentTemplate
pair, creates a deterministic Actor initially suspended, and marks the instance
ready after Substrate accepts it. Readiness of the image was already established
while preparing the ate-api ActorTemplate; AgentInstance creation does not resume
an Actor merely to probe `/readyz`.

Lifecycle operations are implemented as retryable workflows:

- database compare-and-set operations claim a transition;
- network work happens without holding a database transaction or lock;
- completion records the resulting state;
- retries observe and continue the durable phase.

Explicit suspend and resume update the logical lifecycle state. Deletion fences
the instance, deletes the Actor, then removes control-plane state. The workflow
entry points are in
[`go/core/internal/service/agentinstance`](../../go/core/internal/service/agentinstance).

## Automatic quiescence

After an A2A task reaches a quiescent boundary—terminal, `input-required`, or
`auth-required`—the gateway asks the lifecycle workflow to quiesce the Actor.
Quiescence suspends compute and returns the exact snapshot identity while leaving
the AgentInstance logically ready. Substrate ingress resumes a suspended Actor
automatically when the next interaction arrives.

Runtime calls and quiescence are serialized by an in-memory coordinator so a
late suspend cannot race a new turn in one process. This intentionally limits the
gateway to one replica until coordination is moved to a shared store.

```mermaid
sequenceDiagram
    participant Client
    participant Gateway
    participant DB as PostgreSQL
    participant Workflow as AgentInstance workflow
    participant Actor as Substrate Actor
    Client->>Gateway: send or continue A2A task
    Gateway->>Actor: invoke (ingress resumes if suspended)
    Actor-->>Gateway: quiescent event
    Gateway->>Actor: close runtime stream
    Gateway->>Workflow: quiesce instance
    Workflow->>Actor: suspend
    Actor-->>Workflow: exact snapshot identity
    Workflow-->>Gateway: snapshot boundary
    Gateway->>DB: store task + event + snapshot atomically
    DB-->>Gateway: committed
    Gateway-->>Client: publish quiescent event
    Note over Workflow,Actor: AgentInstance remains logically ready
```

## Lost runtimes

Substrate crashes an Actor it cannot bring back — a restore that ran out of
time, a worker that vanished under it, a paused checkpoint whose node is gone —
and a crashed Actor never resumes, so every later message would fail the way
the last one did, after the same wait. When a turn's runtime stream fails, the
gateway asks the workflow whether the runtime is lost: the Actor `CRASHED`, or
gone. If it is, the task fails with the cause under the message prefix
`runtime lost: `, the instance moves to `FAILED` with `failure.reason`
`RuntimeLost` and the same message, and later sends are refused with that
message without dialing the runtime. The transcript stays readable, and the
instance stays deletable.

A turn that ends `input-required` or `auth-required` pauses the Actor where it
ran: a checkpoint on the worker's node that the reply resumes in place. Once a
pause is older than `KAGENT_PAUSED_RUNTIME_TTL` (2m by default;
`controller.pausedRuntimeTTL`; 0 disables) the controller's leader suspends the
runtime the way a terminal turn is quiesced — the checkpoint uploaded to the
snapshot store and recorded as the task's boundary — so a reply after the TTL
restores it on any worker. The sweep touches a paused Actor only while a worker
still runs on its checkpoint's node; a pause on a lost node is the node-loss
handling's to crash.

Deletion suspends a live Actor first, as Substrate's lifecycle contract asks,
and deletes a `PAUSED` or `CRASHED` Actor as it is: a paused Actor's checkpoint
is a node-local copy that suspending would first upload from the node it was
taken on, and when that node is gone the upload never completes. An Actor whose
pre-delete suspend fails — one left suspending on a lost node — is deleted as
it is rather than not at all.

## Runtime boundaries

- Port `8083` serves native gRPC, gRPC-Web, A2A, authenticated MCP, and health.
- Actor A2A gRPC is private on port `80`.
- Runtime readiness is private HTTP `/readyz` on port `8081`.
- ate-api defaults to `dns:///api.ate-system.svc:443`.

Clients never receive Actor addresses. The gateway derives and dials them through
the private atenetwork router.

Every model call a runtime makes names the agent it is made for and the user of
the turn, as request headers: `x-kagent-agent` and `x-kagent-agent-namespace`
carry the `AgentTemplate`'s name and namespace (the controller injects them as
`KAGENT_AGENT_TEMPLATE` and `KAGENT_NAMESPACE`; the Claude harness sends the same
two through `ANTHROPIC_CUSTOM_HEADERS`), `x-kagent-user` the authenticated user
of the turn — the identity the controller resolved from the caller's validated
token and forwarded to the actor as metadata of the same name; absent when
there is none. A gateway between the runtime and the
model provider can attribute usage per agent and user from them, where the
network identity of the caller (a shared WorkerPool pod, an egress) says nothing
about the agent. They are an accounting identity the runtime asserts about
itself, never an authorization input.

Every Actor mounts a Substrate `DurableDir` at `/data`. Harnesses keep private
state there—local framework state, workspaces, and downloaded assets that must
survive Actor replacement. This state is runtime-private; public task history
remains in PostgreSQL.
