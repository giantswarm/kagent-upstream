# Runtime and Lifecycle

An `Session` is PostgreSQL-backed control-plane state exposed through gRPC.
It pins one prepared revision and names one Substrate Actor. It is not a
Kubernetes resource.

## Creation and state

Fresh creation pins the Agent's latest successful runtime revision. Pending or
failed edits leave that revision available; an Agent with no successful revision
cannot create sessions. Creation retries retain the existing session's pinned
revision. Explicit egress grants are applied from the controller's persisted
intended policy before execution. Lifecycle
operations do not read or translate Kubernetes policy.

The control plane creates a deterministic Actor initially suspended and marks the session
ready after Substrate accepts it. Readiness of the image was already established
while preparing the ate-api ActorTemplate; Session creation does not resume
an Actor merely to probe `/readyz`.

A create may name a volume source: an existing read-write-many volume by its
CSI driver and handle, and the mounts of it the session's actor gets, each a
clean relative directory of the volume at a clean absolute path outside `/data`
(the durable directory, which its snapshot tars on every commit), at most one
of them read-write: the session's own directory, whose sub-path names the
session as `${SESSION_ID}`. kagent never creates or deletes the volume. The
source is part of the session row and shows on get and list; a retry under the
same request ID must name the same source. The request's admission token is the
proof the installation's configured `VolumeAdmission` verifies for that source;
the service hands it to the admission and to nothing else, so it is never
stored or shown. Without a configured admission every create that names a
source or carries a token is refused `FailedPrecondition`, so no caller mounts
a volume of its choosing. A create without the fields is handled as before. An
`Agent` or `AgentTemplate` carries no volume: the session is the only place one
is named. The session's actor does not mount the source yet: that needs
Substrate to mount an existing volume at a sub-path, and until then an admitted
source is stored and shown for that later step.

### Session volume admission

The built-in admission is `jwt` (`go/core/internal/service/session/jwtadmission`),
configured by the controller's `sessions.admission.jwt` (`jwksUrl`, `issuer`
and `audience`, all three required once it is set). `jwksUrl` must be `https`,
since whoever can rewrite the keys in transit can sign grants; a plain `http`
URL is admitted only when `allowHttp` is set, for an in-cluster issuer that
serves no TLS. Without it the controller logs the error and keeps running on an
admission that refuses every volume source `FailedPrecondition`, so a
misconfigured issuer costs the volume sources, never the controller. The
admission token
is a short-lived JWT signed by an issuer the installation trusts (ES256,
ES384, RS256 or PS256, with a `kid`), which checks before signing that the
caller may work on the volume. With the default claim names its payload is:

```json
{
  "iss": "<issuer>", "aud": "<audience>", "sub": "<the caller>",
  "exp": 0, "nbf": 0,
  "agent_namespace": "<the namespace of the session's Agent>",
  "volume": {"driver": "nfs.csi.k8s.io", "handle": "<the driver's volume handle>"},
  "mounts": [
    {"sub_path": "sessions/${SESSION_ID}"},
    {"sub_path": "mirrors", "read_only": true}
  ],
  "changed": ["<owner>/<repository>"],
  "egress": [
    {"origin": "https://git.example.com",
     "authentication": {"header": "authorization", "prefix": "Basic ", "credential": "git.example.com/git"}},
    {"origin": "https://api.example.com",
     "authentication": {"header": "authorization", "prefix": "Bearer ", "actor_jwt": {"audiences": ["https://api.example.com"]}}}
  ]
}
```

The claims that carry the Agent namespace, the volume, the mounts, the
changed repositories and the egress are the installation's to name, after its
issuer's grants: `sessions.admission.jwt.claims.{agentNamespace, volume,
mounts, changed, egress}`, by default `agent_namespace`, `volume`, `mounts`,
`changed` and `egress`. Their shapes are fixed; a name may be neither a
registered claim nor another of the five.

It is admitted when the signature verifies against a key of the issuer's JWKS,
`iss` and `aud` are the configured ones, `exp` has not passed and `nbf` has
(30 seconds of leeway), `sub` is the caller as the authenticator identifies it
(`server.auth.userIdClaim`), the grant's Agent namespace is the namespace of
the session's Agent (a grant that names none is refused: it would admit an
Agent in any), the grant's volume is the request's, and every
requested mount's sub-path is one the grant allows, read-only where the grant
says so. `changed` lists repositories, relative to the session's working
directory, whose origin moved since the volume's last sync, for the session's
runtime to fetch before its first turn; it is stored on the session as
`changed` and shown on get and list. The token itself is stored and shown
nowhere.

### Session egress

`egress` lists the origins the session's own actor may reach beyond its
Agent's egress, in the vocabulary of an `EgressPolicy` destination: an exact
`https` origin and, optionally, the header the egress gateway sets on every
request to it. The Agent's other sessions get none of them, and the credential
never reaches the actor, the controller or its database. A header's source is
one of:

- `credential`: a name at the installation's session credential provider,
  `sessions.admission.jwt.credentialAuthority`
  (`ate-secret://<provider-class>/<provider-name>`). The session stores
  `<credentialAuthority>/<credential>` as the header's `credential_uri`; the
  gateway resolves it through that provider with the actor's verified
  identity, so the provider answers for the session it was admitted for. A
  grant never names the provider, and Substrate's Kubernetes Secret provider
  (`k8s.io`) is refused as the authority: a grant never names a Secret. Without
  an authority, a grant that names a credential is refused
  `FailedPrecondition`.
- `actor_jwt`: the actor's own JWT, minted by Substrate per request (valid ten
  minutes) and bound to `audiences`, for a service that verifies the actor
  itself or exchanges the JWT for a token of its own (RFC 8693).

The admission refuses an origin that is not an exact `https` DNS origin, an
invalid or routing header, a header with no source or two, a credential name
that is not a clean relative path, a header granted twice on one origin and
more than 16 destinations. The egress is stored on the session as `egress` and
shown on get and list. Every time the session's actor policy is applied (on
create, resume and every change of its Agent's egress), each origin is added
over HTTPS beside the revision's and its header beside the Agent's own; a
session header on an origin and header the Agent already sets, or an `https`
origin on a host and port the Agent reaches over plain `http`, fails the
application: the session never takes over a credential its Agent binds.

Every refusal is `PermissionDenied` with its reason (no grant, expired, not yet
valid, another person's, another namespace, another volume, a mount it does not allow, an unknown
issuer, audience or key, an invalid signature, a malformed grant) and never
key material. The JWKS is fetched on the first grant, trusted for ten minutes,
and fetched again on a key id it lacks (at most every ten seconds), so a key
rotation needs no restart. An issuer whose JWKS cannot be fetched refuses
`Unavailable`, not `PermissionDenied`: the grant may well be valid, and the
caller retries.

Substrate v0.6.0-alpha3 requires protocol-specific egress policies and permits
DNS queries only for hostnames matched by those policies. Kagent allows
each configured HTTP(S) origin, preserving its scheme, DNS name, and port, and
replaces credential headers in that destination's deciding rule. Conflicting
protocols on the same host and port are rejected before Actor creation. Literal
IP allowlists are unsupported by this Substrate release; model, MCP, and telemetry endpoints
must use DNS names. Host-based test services use Kubernetes Services and
EndpointSlices to provide those names.

Runtime endpoints must use full DNS names, such as
`kagent-controller.kagent.svc.cluster.local`. DNS search-domain expansion of a
short service name would query a hostname absent from the egress policy. The
Helm chart uses `global.clusterDomain` (default `cluster.local`) for its generated
controller and API tool URLs; custom model, MCP, and telemetry URLs must also
use full names.

Create (including forks), explicit Suspend, Resume, and Delete keep their current
operation UUID and executor claim on the session row. Fork creation loads its
pinned checkpoint from PostgreSQL. Namespace provisioning belongs to the Agent
controller; session creation uses the pinned ActorTemplate's existing namespace.
Read-only preparation may run concurrently, but an atomic execution claim permits
one bounded inline attempt to issue runtime mutations. Network work holds no database
transaction or lock. Completion changes the session atomically and retains its
operation UUID until a later transition supersedes it.

A joined caller observes the current session only while its admitted generation
remains current. A superseded caller gets a conflict and issues no runtime work,
even when the new operation has the same state and kind. There is no historical
lifecycle result archive or pruning requirement. Creation retries return current
session state; already-at-target Suspend/Resume requests are successful no-ops.
Neither requires an old operation receipt. A2A task storage and checkpoint
reconstruction have their own durable history requirements.

An unclaimed operation can retry preparation; Delete may supersede it. Every
admitted Session deletion enters DELETING. Preparation failure invalidates its
generation but leaves DELETING in place, keeping task admission closed until a
delete retry completes. Explicit deletion requires a client retry; idle deletion
is retried by the expiration worker. After an attempt issues runtime work, errors
retain the operation and resource pins. The attempt releases its execution claim
on return. A crashed executor's claim expires after at most two minutes, allowing
a client retry to claim the same operation with a new executor ID. Each attempt
uses a context bounded by that interval; no claim renewer or recovery sweep runs.
A stale executor cannot complete or release a newer attempt's claim.

Lifecycle retries inspect the pinned Actor identity and continue Substrate's
reentrant workflows, including completing egress setup for an existing creation.
Already completed creation steps are not repeated. Fork retries must still match
the retained source snapshot. Conflicting Session lifecycle, task, and checkpoint
work remains blocked until the pending operation completes. Releasing an attempt
never clears its durable intent or makes uncertain work an unissued preparation.

Clients must retry the mutation after transient errors; Get only observes it.
If the client stops retrying, the explicit operation remains pending across API
restarts. There is no general automatic lifecycle recovery. The
[client retry contract](../lifecycle-retries.md) covers error codes, deadlines,
creation request IDs, and the limitations of retries and concurrent callers.

Explicit deletion retains an indefinitely kept DELETED session tombstone with its owner,
creation request identity, and final operation UUID. It clears runtime routing,
releases the revision and checkpoint pins, and revokes shares atomically. A fork's
source checkpoint UUID remains as request identity; a generated foreign-key column
pins that checkpoint only while the session is live. Ordinary session/task/share
access excludes deleted sessions. Create/Fork request IDs remain reserved after explicit
deletion and cannot recreate compute. Public Delete still returns NotFound for a
fresh request after deletion; already-authorized joined Delete callers can observe
the final tombstone. No public operation API is introduced.

Explicit suspend and resume update the logical lifecycle state. Deletion closes task
admission, stops and deletes the Actor, then tombstones the session. The workflow
entry points are in
[`go/core/internal/service/session`](../../go/core/internal/service/session).
TaskStore writes and automatic idle lifecycle work use durable task boundaries
alongside these explicit lifecycle claims. Checkpoint reservations also block
conflicting task writes and idle lifecycle work.

The unreleased schema requires a clean database. Do not overlap older binaries that
can issue lifecycle calls without session-local execution claims. PostgreSQL tests with controlled
Actor responses verify claim ordering, delayed callers, and lost responses; live
Substrate settlement and the complete multi-replica rollout remain acceptance work.

## Idle expiration

Sessions expire after seven days without task activity by default, using the same
bounded deletion workflow as Sandboxes. `config.sessions.idleTtl` in Helm configures
the controller. Values use protobuf durations (`604800s` for seven days,
`2592000s` for thirty); `0s` disables the worker, including retries, and negative values
are rejected. There is no per-agent override or maximum TTL.

Idle time is measured from the later of session creation and the latest stored
A2A event's database timestamp. Forks retain original event timestamps, so their
own creation time gives them a full idle lifetime. Reads, renames, lifecycle calls,
and retried writes or settlement do not extend it. Running tasks and
INPUT_REQUIRED/AUTH_REQUIRED turns never expire, even after that duration. Pending
lifecycle operations, dispatch reservations, native cleanup, claimed suspend
work, and checkpoint creation also block expiration.

A leader-only worker scans once per minute in bounded pages. Under the same
PostgreSQL row lock used by task admission, it rechecks the idle clock and active
work, then admits a normal DELETE operation with the durable reason `idle_timeout`
and moves the session to DELETING. A turn admitted after the scan but before the
lock prevents expiration. Once expiration is admitted, new task execution is
fenced, including after preparation failures.
The worker uses the existing Actor deletion workflow and execution claims, so
runtime I/O holds no database locks and overlapping attempts cannot issue the
same generation concurrently. Failed expiration resumes on later sweeps or after
leader replacement; changing a nonzero TTL does not cancel admitted deletion.

After runtime deletion succeeds, the normal delete completion transaction removes
the session, shares, runtime row, and creation receipt. GetSession returns NotFound;
CreateSession or ForkSession with the same caller/request ID can create a fresh
session. Explicit client deletion still retains its tombstone and request ID.

A2A continuation using a missing or expired context ID returns `TaskNotFound`
(JSON-RPC `-32001`, gRPC `NOT_FOUND`), matching continuation using an old task ID.
Ownership-filtered lookup gives the same response for another user's Session;
the response does not distinguish expiry from absence or hidden ownership.
Authorization-check failures still return permission denied. Clients can start a
new conversation after `TaskNotFound` by sending without a context or task ID.
Service-level not-found errors use this same A2A mapping, including missing Agents.
Agent-card HTTP discovery returns 404 for not-found errors and 403 for permission
denials. Starting a new conversation requires an existing, authorized Agent.

The retention rule preserves the existing audit policy: A2A context, tasks, event
history, and explicit checkpoints remain in PostgreSQL. Retained checkpoints can
still be forked after the source session expires and retain their own snapshot
pins. The ordinary Actor deletion workflow removes the session's runtime and
releases its revision/checkpoint references; backend snapshot garbage collection
remains governed by Substrate and retained checkpoint pins. Idle expiration does
not bound audit-history or explicit-checkpoint storage; those need a separate
retention policy. History is no longer accessible through the expired Session API.

`kagent.session.expired` counts completed sweep removals (Prometheus exports
`kagent_session_expired_total`). Each removal logs `expired idle session` at debug
level with `session_id` and `idle_time`.

## Automatic quiescence

The runtime stages a final task update and acknowledges it after native cleanup.
That acknowledgement publishes task state and history atomically, without waiting
for suspension. A Session lifecycle worker independently claims the idle
boundary in PostgreSQL. Both INPUT_REQUIRED/AUTH_REQUIRED and terminal work
suspend the Actor and record the exact durable VOLUMES snapshot. Waiting tasks
are not forkable. The Session stays logically READY, and Substrate ingress
resumes it when another authorized interaction arrives.

Unfinished native cleanup blocks new task writes and explicit lifecycle changes.
After publication, a new turn may supersede idle work before it is claimed. Once
claimed, idle work blocks new execution, explicit lifecycle changes, and checkpoint
capture until its outcome is recorded. The worker performs runtime I/O outside the
database transaction. Successful snapshot references are retried on database failure
without repeating the Substrate operation. Checkpoint creation requires the matching
snapshot and can return FailedPrecondition after task completion while it is pending.

After settlement commits, a bounded, nonblocking in-memory signal wakes the local
workers. Signals coalesce; four workers drain durable claims, handing off a wake-up
before runtime I/O so a burst can use all four workers. PostgreSQL claims coordinate
replicas. Each worker scans at startup and, when idle, every minute by default
(`sessions.quiescenceRecoveryInterval` in controller config). A crash between commit and signaling can
leave an actor running until a surviving replica's next recovery scan; increasing
the interval increases that delay and the time until its snapshot is available.
Ordinary committed settlements do not wait for the recovery interval.

An empty claim is followed by a check for unclaimed work on ready sessions. If
dispatch, checkpoint creation, a lifecycle operation, or a row lock blocks that
work, workers retry within one second until it becomes eligible or is superseded.
Database errors also use this short retry. Explicit lifecycle completion wakes
workers when it returns a session to READY. Suspended/deleted sessions and claimed
work do not keep the short retry active. Idle workers make one claim attempt and
one pending-work check per recovery interval, rather than querying every second.
Other background workers and connection-pool idle settings still affect whether
PostgreSQL can become idle.

Unclaimed idle work survives API restarts. A claim for possibly issued runtime work
never expires: losing the worker does not prove that the suspend stopped. Uncertain
claims still block new work, but completed results remain readable. The recorded
actor UID is checked before lifecycle calls; a same-name replacement cannot be
adopted implicitly.

```mermaid
sequenceDiagram
    participant Client
    participant Gateway
    participant Actor as Agent runtime
    participant API as TaskStore API
    participant DB as PostgreSQL
    participant Worker as Session lifecycle worker
    Client->>Gateway: authorized send / continuation
    Gateway->>Actor: invoke
    Actor->>API: create and versioned updates
    API->>DB: stage final boundary
    API-->>Actor: committed version
    Actor->>API: settle after native cleanup
    API->>DB: publish task/history atomically
    API-->>Worker: nonblocking local wake-up
    Actor-->>Gateway: final event
    Gateway->>Actor: close observer connection
    Gateway->>DB: observe publication
    Gateway-->>Client: current public task
    Note over Worker,DB: Idle lifecycle runs independently of the client response
    Worker->>DB: claim idle boundary unless new execution superseded it
    Worker->>Actor: suspend with VOLUMES snapshot
    Actor-->>Worker: suspended Actor and durable VOLUMES snapshot
    Worker->>DB: record snapshot and finish idle claim
```

## Runtime boundaries

- Port `8083` serves native gRPC, gRPC-Web, A2A, authenticated MCP, and health.
- Actor A2A gRPC is private on port `80`.
- Runtime readiness is private HTTP `/readyz` on port `8081`.
- ate-api defaults to `dns:///api.ate-system.svc:443`.

Clients never receive Actor addresses. The gateway derives and dials them through
the private atenetwork router.

Every Actor mounts a Substrate `DurableDir` at `/data`. Harnesses keep private
state there—local framework state, workspaces, and downloaded assets that must
survive Actor replacement. This state is runtime-private; public task history
remains in PostgreSQL.

The ADK's built-in file and shell tools use `/data/workspace`, shared by the root
agent and subagents in one Actor. Workspace files survive VOLUMES snapshots, resume,
and checkpoint forks. Skills are discovered independently; the workspace has no
session-specific directories or mutable skills symlinks. The workspace is created
on first use, with no predefined upload or output directories. File tools accept
normal relative and absolute paths inside the sandbox; Substrate owns isolation.

Each bash invocation starts a fresh shell with the Actor environment. The working
directory and configured skills directory are added to `PYTHONPATH`. Commands use
a 60-second default timeout; `timeout_seconds` can select up to 600 seconds.
Stdout and stderr are combined, captured up to 64 KiB, and returned with an
explicit truncation marker. Large results should be redirected to workspace files.
Commands and their process groups are stopped when a call finishes, times out,
or is canceled. Background processes are not durable work and have no process
handle API in the bash tool. Suspending an Actor preserves files, not running
commands. Command payloads and output are not logged by the shell helpers.

Substrate v0.6.0-alpha3 uses snapshot fidelity. Agent and standalone sandbox
templates use VOLUMES. Resume starts fresh containers
from the OCI image with the saved durable directories. Harnesses recover pending
approvals and user input from their durable state; sandbox guests start with a
fresh process registry. Agent checkpoints can be forked without carrying the
source session's process memory or runtime identity into the new session.

Kagent selects the highest snapshot generation and accepts its object-storage
URI only after its durable status is COMPLETED. A pending generation cannot
fall back to an older checkpoint. Suspension requires a durable VOLUMES snapshot;
ROOTFS and MEMORY snapshots are rejected because they restore additional runtime
state.

Codex and Claude record failed deferred continuations as `abandoned` in their
existing input journals. Admission rejects abandoned or restored unfinished
execution phases with an error requiring a new session, instead of restoring a
pending interaction for a failed task. Failures known to precede execution keep
the existing retry behavior. This rule is limited to durable deferred input;
general crash recovery and remote tool outcome reconciliation are follow-up work.

The Go ADK opens and migrates its SQLite session store before readiness, but
retains no idle database connections. MEMORY and golden restores preserve guest
memory while rematerializing `/data`, so a connection opened before the snapshot
can retain a stale file identity and reject writes with `SQLITE_READONLY_DBMOVED`.
Closing connections when returned to the pool keeps quiescent snapshots free of
database handles; each later operation opens the current backing file.

## Runtime revision cleanup metrics

GC uses the controller's shared OpenTelemetry provider and configured OTLP export.
Prometheus scraping is opt-in through `controller.metrics.enabled`.

| OTel metric | Instrument / unit | Prometheus name | Meaning |
| --- | --- | --- | --- |
| `kagent.runtime_revision.gc.pending` | Observable integer gauge / `{revision}` | `kagent_runtime_revision_gc_pending` | Eligible persisted revisions from the last successful discovery. No application attributes. |
| `kagent.runtime_revision.gc.duration` | Histogram / `s` | `kagent_runtime_revision_gc_duration_seconds` | Each discovery or collection attempt, including claim, Substrate read/delete, and finalization. `kagent.gc.stage=discovery\|collection`; `error.type` only on failure: a Substrate gRPC code name or `_OTHER`. Parent cancellation is excluded; operation deadlines count as failures. |

Pending is absent before successful discovery, on standby replicas, and after GC
stops. Do not fill absence with zero: zero means a successful empty discovery.
Discovery errors retain the last count. Scrapes only read the cache; restart
reconstructs pending from PostgreSQL and resets process-local histogram totals.

- **Growing pending:** compare attempt rates, failure ratios, and latency on the
  active controller before diagnosing churn versus slow or failing cleanup.
  Let GC retry; never bypass reference/UID protections or clear deletion markers.
- **Rising failure ratio or latency:** use reset-aware `rate` on histogram
  `_count` (failed attempts have `error_type`), grouped by `kagent_gc_stage`,
  and `_bucket` quantiles. Discovery errors point to the database; collection
  errors require checking the bounded error type and logs (`revision`,
  `actor_template_atespace`, `actor_template_name`, `error`) to identify the
  failing dependency and repeated same-object failures.
