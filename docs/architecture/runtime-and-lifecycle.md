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

Create (including forks), explicit Suspend, Resume, and Delete keep their current
operation UUID and executor claim on the instance row. Fork creation loads its
pinned checkpoint from PostgreSQL. Namespace provisioning belongs to the template
controller; instance creation uses the pinned ActorTemplate's existing namespace.
Read-only preparation may run concurrently, but an atomic execution claim permits
exactly one caller to issue runtime mutations. Network work holds no database
transaction or lock. Completion changes the instance atomically and retains its
operation UUID until a later transition supersedes it.

A joined caller observes the current instance only while its admitted generation
remains current. A superseded caller gets a conflict and issues no runtime work,
even when the new operation has the same state and kind. There is no historical
lifecycle result archive or pruning requirement. Creation retries return current
instance state; already-at-target Suspend/Resume requests are successful no-ops.
Neither requires an old operation receipt. A2A message deduplication and event
replay have their own durable history requirements and are unchanged.

An unclaimed operation can retry preparation; Delete may supersede it. Preparation
failure invalidates its generation. Once claimed, an operation never expires. A
timeout, disconnected client, process restart, lost runtime response, or runtime
success whose database completion fails leaves the operation pending and blocks
conflicting lifecycle work, including Delete. The instance retains its revision
and checkpoint pins. Only the original executor with a known successful response
may finish persistence; a favorable Actor read alone does not establish that an
earlier request has stopped. There is no automatic takeover or administrative
unlock API for uncertain operations.

Deletion retains an indefinitely kept DELETED instance tombstone with its owner,
creation request identity, and final operation UUID. It clears runtime routing,
releases the revision and checkpoint pins, and revokes shares atomically. A fork's
source checkpoint UUID remains as request identity; a generated foreign-key column
pins that checkpoint only while the instance is live. Ordinary instance/task/share
access excludes deleted instances. Create/Fork request IDs remain reserved after
deletion and cannot recreate compute. Public Delete still returns NotFound for a
fresh request after deletion; already-authorized joined Delete callers can observe
the final tombstone. No public operation API is introduced.

Explicit suspend and resume update the logical lifecycle state. Deletion closes task
admission, stops and deletes the Actor, then tombstones the instance. The workflow
entry points are in
[`go/core/internal/service/agentinstance`](../../go/core/internal/service/agentinstance).
This serialization covers explicit lifecycle calls only: A2A, Pause, Quiesce, and
checkpoint execution still need shared ownership before multi-replica gateway use.

The unreleased schema requires a clean database. Do not overlap older binaries that
can issue lifecycle calls without instance-local execution claims. PostgreSQL tests with controlled
Actor responses verify claim ordering, delayed callers, and lost responses; live
Substrate settlement and the complete multi-replica rollout remain acceptance work.

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

A turn's runtime boundary never holds the turn back. When the quiesce after a
finished turn or the pause for input fails, the gateway logs the failure with
the instance and the task and stores the event without a snapshot: the task is
final or waiting, and the instance takes the next message, which resumes the
runtime in whatever state the failed call left it. The same sweep retries the
quiesce of a final turn that has no snapshot once it is older than the TTL;
Substrate answers the suspend of an Actor that is already suspended with its
snapshot, which the sweep records as the turn's boundary.

A turn records its events through the gateway run that dispatched or observes
it. A runtime lost after the turn's first event, or a controller restarted
mid-turn, ends that run and leaves the task `working`, the instance's active
task for good. Once a working task has recorded no event for longer than
`KAGENT_STALLED_TURN_TIMEOUT` (1h by default; `controller.stalledTurnTimeout`;
0 disables) and no run of the gateway follows it, the controller's leader fails
it as interrupted, and the instance takes the next message.

## Deleted agents

Deleting an AgentTemplate retires its AgentTemplate/Harness pair, and its
instances would otherwise be left alone: an instance READY on its revision keeps
its Actor in Substrate and the revision's ActorTemplate referenced, so a
conversation nobody deletes outlives its agent and the runtime revision GC never
collects the template. The controller's leader sweeps the instances whose agent
is gone — no active pair at the template name and harness name their revision was
rendered for — every `DefaultDeletedAgentSweepInterval` (5 minutes) and deletes
each through the ordinary delete workflow: its runtime is suspended and deleted,
its revision released, so the GC then collects the revision and its ActorTemplate.
The instance's quiesce lock is taken without waiting, so a turn being dispatched
in this process wins and the next sweep looks again; an admission a lifecycle call
or an unsettled turn refuses is retried the same way. An instance of an agent
re-rendered, or deleted and created again under the same name, keeps its pinned
revision: a pair at that name is active, so it is not swept, and the superseded
revision sweep moves it onto the current revision instead.

## Superseded revisions

An instance is prepared on one runtime revision of its agent and its Actor
runs on that revision's ActorTemplate. When the agent is re-rendered — a
template change, a Harness change, a release that renders every template
anew — the agent's current revision moves on and the instance's does not: a
revision rendered by an older release lacks what the platform needs now, and
its ActorTemplate keeps what that release put there. A suspended Actor can be
moved: Substrate's `UpdateActor` accepts another ActorTemplate while the Actor
is suspended, as long as the sandbox config and the durable volumes stay the
same, and the next resume restores the Actor's data onto the new template's
golden snapshot. The workflow moves an Actor onto its agent's current revision
in the same atespace, gives it that revision's egress allowlist, and records
the instance as prepared on it, so the superseded revision loses its last
reference and the runtime revision GC deletes it with its ActorTemplate.

Three paths move an Actor: a turn, before the gateway wakes the quiesced
runtime; the Resume RPC of a suspended instance; and the controller leader's
sweep every `KAGENT_REVISION_REPOINT_INTERVAL` (5m by default;
`controller.revisionRepointInterval`; 0 disables), which visits every READY
instance on a superseded revision, so a conversation nobody writes to again
does not keep the old revision alive. The turn's move and the sweep's may meet
on one Actor: whichever comes second finds the Actor moved and records what
the first has yet to. A lifecycle operation that claims the instance meanwhile
wins, and the Actor goes back to the template of the revision the instance
records. A template Substrate refuses — different sandbox config, a durable
volume added or changed — is logged and the instance stays on its revision;
a live or paused Actor is never moved under its turn.

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
