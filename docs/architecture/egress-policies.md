# Egress policies

`EgressPolicy` grants additional HTTP(S) and TLS passthrough access to an
Agent or standalone sandbox. An Agent selects policies through `spec.egressPolicyRefs`; a
SandboxTemplate selects them through the same field. References resolve in the
workload's namespace. Policies are managed through the Kubernetes API.

AgentTemplate owns reusable behavior, and Harness owns how it runs. Neither
selects egress policies. This lets two Agents reuse a template while granting
different access for their tasks. A shared child template contributes its
inferred model, MCP, and artifact destinations, but cannot select additional
policies. The root and all shared children execute in one Actor and share its
complete grant; there is no per-child network isolation.

## Grant access for a task

For example, a dependency-installation policy can grant access to registries:

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: EgressPolicy
metadata:
  name: node-dependencies
  namespace: team-a
spec:
  destinations:
    - origin: https://registry.npmjs.org
    - origin: https://nodejs.org
---
apiVersion: api.kagent.dev/v1alpha3
kind: Agent
metadata:
  name: code-reviewer
  namespace: team-a
spec:
  templateRef:
    name: code-review
  harnessRef:
    name: codex
  egressPolicyRefs:
    - name: node-dependencies
```

An origin is a scheme, DNS name, and optional port. Omitted ports mean 443 for
HTTPS and 80 for HTTP. Every path on that origin is allowed. URL paths, queries,
fragments, user information, IP addresses, and other schemes are rejected.
A wildcard replaces exactly one leftmost label: `https://*.example.com`
allows `api.example.com`, but neither `example.com` nor `a.b.example.com`.
`*` and `*.com` are not accepted. Redirects and downloads from other origins
require their own grants. A registry grant is not a guarantee that every
package's install script or release download can reach its dependencies.

## Reach a TLS service that is not HTTP

A `tls://` origin allows TLS that the gateway forwards without decryption, for
a database or message broker that speaks TLS:

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: EgressPolicy
metadata:
  name: orders-database
  namespace: team-a
spec:
  destinations:
    - origin: tls://orders-db.example.com:5432
    - origin: tls://*.kafka.example.com:9093
```

The port is required; the hostname follows the rules of an HTTP(S) origin,
wildcard included. The gateway matches the SNI server name and port of each
connection's ClientHello and reads nothing after it: a client that sends no SNI,
and a protocol that upgrades to TLS after a cleartext exchange (STARTTLS), do
not match. A passthrough destination cannot carry authentication, and its
hostname pattern and port cannot also be an HTTP or HTTPS origin, because
Substrate selects the three rule kinds by the same hostname pattern and port. A
credential binds only to a hostname with an HTTPS origin, never to one a
passthrough origin alone allows. A model, MCP server or artifact URL never
infers a passthrough grant. A policy change reaches an open passthrough
connection only when it ends.

For a standalone sandbox, select the policy on its template:

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: SandboxTemplate
metadata:
  name: node-tools
  namespace: team-a
spec:
  workload:
    image: registry.example.com/tools@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  substrate:
    workerPoolRef:
      name: default
    snapshotPolicy:
      location: s3://example/snapshots/
  egressPolicyRefs:
    - name: node-dependencies
```

`Agent.spec.egress` lists origins inline on a single Agent, `tls://` destinations
included; an EgressPolicy is the reusable, credential-bearing form, and the two combine.

Each workload can select up to 16 policies, each containing 1–64 destinations.
The compiler combines them with inferred integration access, removes equivalent
origins, and orders the result deterministically. The final Actor policy is
limited to 256 rules. References are additive; they cannot restrict inferred
model, MCP, artifact, or control-plane access. No references means no additional
access. A standalone sandbox with no references has no allowed egress.

## Call an API with a credential

Policies authenticate requests using a header, an optional prefix, and a Secret
in the same namespace. Authentication requires an exact HTTPS origin, and binds
the credential to that origin's hostname
(`ate-secret://k8s.io/default/<policy namespace>/<Secret>/<key>`):

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: EgressPolicy
metadata:
  name: ticket-api
  namespace: team-a
spec:
  destinations:
    - origin: https://tickets.example.com
      authentication:
        header: Authorization
        prefix: "Bearer "
        secretKeyRef:
          name: ticket-auth
          key: token
    - origin: https://search.example.com:8443
      authentication:
        header: X-API-Key
        secretKeyRef:
          name: search-auth
          key: api-key
```

The gateway sets the header value to `prefix + secretValue`. Prefixes are literal:
include the trailing space in `"Bearer "`. Omitting the prefix or setting it to
an empty string uses the Secret value directly, as with an API-key header.

Create these Secrets separately. The compiler validates that each Secret and key
exists, but persists only the reference. Substrate's credential provider fetches
the value and the gateway replaces the request header. Scripts and SDKs must
send that header, using an inert placeholder:

```sh
curl -H 'Authorization: Bearer kagent-credential-injected' \
  https://tickets.example.com/tickets
curl -H 'X-API-Key: kagent-credential-injected' \
  https://search.example.com:8443/search
```

The gateway replaces an existing header; it does not add a missing one. Policy
selection does not automatically configure arbitrary tools or environment
variables. Clients must honor the projected gateway trust bundle. See
[credential injection](credential-injection.md) for provider namespace grants,
CA setup, inferred SDK placeholders, and rotation behavior.

Bindings use the hostname and a case-insensitive header name, as every other
gateway credential does: a binding applies to each allowed port of its hostname.
Identical bindings merge; conflicting Secret references or prefixes for the same
hostname/header fail preparation. Explicit and inferred bindings follow the same
rule. Caller-token passthrough cannot share its hostname with static gateway
credentials. Routing, framing, hop-by-hop, proxy-authentication, and Cookie
headers cannot carry credentials. At most 16 credential headers are supported per
hostname. Use distinct DNS names for origins that need different credentials.

`RemoteMCPServer.spec.headersFrom` applies the same credential-header rules to
Secret-backed entries during Kubernetes admission. Literal and ConfigMap-backed
headers do not use gateway credential injection. A server may configure up to
64 headers, with names of at most 256 characters.

An exact credential-bearing rule wins over a matching wildcard rule. Credentials
are never attached to wildcard rules. HTTP, HTTPS and TLS passthrough cannot
share a hostname pattern and port, because Substrate cannot distinguish that
rule selection.

## Preparation and lifecycle

KRT watches selected policies and their Secrets independently of runtime
compilation. Each policy resolves separately. A missing or deleting policy,
missing Secret key, or invalid policy contributes no access; grants from the
remaining policies are still considered. Invalid reference names or more than
16 references revoke all additional access. Grant errors never block runtime
compilation or change revision identity.

Both Agent and SandboxTemplate expose an independent `EgressPoliciesApplied`
condition:

| Status | Reason | Meaning |
| --- | --- | --- |
| True | `Applied` | All requested grants are admitted. |
| False | `EgressPolicyNotFound` | A referenced policy is missing or deleting. |
| False | `InvalidEgressPolicy` | A policy, Secret reference, or reference list is invalid. |
| False | `EgressGrantConflict` | New grants are held; still-requested, compatible previous grants remain. |

Messages identify affected policies and conflicts. A conflict takes precedence
when there are also resolution errors. This condition never affects `Ready` or
`ResolvedRefs`; missing policies no longer fail runtime reference resolution.
It reports admission from the controller's current inputs, not acknowledgement
that every actor has received the update.

Runtime revisions retain the integration access inferred from models and tools,
including credential references and caller-token passthrough constraints. This
pinned access is the revision's base.
The combined additional access from a source's referenced EgressPolicies is its
`EgressGrant`: destinations it may contact and credentials the gateway may inject.
Reconciliation and source definition structs carry it as `EgressGrant egress.Access`.
Revision identity covers compiled runtime inputs and the owning Kubernetes UID.
The full source specification is preserved as provenance for the first preparation
of that revision. Explicit policy grants and policy references do not enter
revision identity.
Changing a grant or its Secret reference does not prepare a new runtime image.
Secret values remain outside compiled inputs; the gateway handles value rotation.

The elected controller's Agent and SandboxTemplate reconcilers resolve current
grants independently of runtime compilation. A missing Harness, AgentTemplate,
or WorkerPool cannot freeze a revocation. Each preparation queue is the sole
writer of its source definition's grants in PostgreSQL. Definition updates
persist admitted grants and retire replaced Kubernetes UIDs atomically.

The stored grant set is the last good set: valid on its own and compatible with
the desired revision's base when compilation succeeds. If the complete candidate
conflicts, preparation retains only previously admitted destinations and identical
credential bindings that are still requested, filtering any that conflict with
the desired base. Removals apply immediately; all additions wait until the
conflict is fixed. A new Kubernetes UID inherits no grants. When unrelated
compilation fails, admission checks grants on their own and still applies
revocations.

A single store transaction records the runtime revision and, when ready, promotes it if
the active source still desires it. Failed runtime preparation preserves
the last successful revision while continuing to update source grants.

Effective policy is computed when read: the revision's pinned integration access
is merged with grants from its active source UID. Retired or replaced identities
contribute no grants. There is no separately published per-revision policy and
no notification needed when a revision is recorded: every read joins against
the current persisted grants, including for newly recorded revisions.

Every preparation persists the source definition, then pages its allocated actors
across all retained revisions and Kubernetes UIDs and applies effective policy
with at most 16 actor updates in parallel. Unchanged policies skip the Substrate
write. Failures return to the existing preparation queue, which retries the whole
source with exponential backoff even when grants have not changed. Slow actor
updates therefore delay further work on that preparation queue. Startup replays
every Agent and SandboxTemplate through preparation; no separate actor sweep is
needed.
After its input cache syncs, Agent preparation also enqueues stored definitions
whose Agents are absent from watched namespaces. Retired identities are included
only while they have non-deleted instances, so a restart retries interrupted
revocations without re-enqueueing every historic source. Agent deletion does not
need a finalizer. SandboxTemplate preparation retains its existing
finalizer until retirement and actor updates succeed.

Running and suspended actors receive updates without replacement or a new
invocation. Missing or invalid policies and Secrets revoke only the affected
policies' grants. Actors on older revisions can have different pinned access:
their effective policy drops only the destinations or credentials that conflict
with that base, caller-token origins, or combined limits. Nonconflicting grants
remain. The pinned base always wins; dropping a credential retains its network
destination if the destination itself is compatible. Missing revisions or malformed persisted inputs return errors and
block execution.

New Sessions and sandboxes pin the latest successful runtime revision, including
while a configuration edit is unresolved, preparing, or failed. Initial creation
still requires a successful revision. Existing instances and checkpoint forks
retain their runtime configuration.

Create, creation retries, and resume load and apply the effective policy from
persisted revision inputs and source grants. They do not read or translate
Kubernetes policy. The workflow creates a suspended Actor and installs its
`default` egress policy before it can
run; a fork cannot restore an old policy from a snapshot. Applications read the
Substrate policy version before loading intended policy, then write using its UID
and version preconditions. Conflicts reload both. After applying, they recheck
intended policy to catch updates whose actor discovery raced with creation.
Missing revisions or malformed persisted inputs block execution and are retried.
No database transaction or lock is held across Substrate calls.

Policy convergence is asynchronous: a Kubernetes write is not a synchronous
revocation barrier. Once the gateway observes the changed policy, subsequent
requests use it. An update does not cancel a request already in flight. Golden
Actor preparation and artifact-startup egress are separate from this API's runtime
Actor access.

Revisions prepared before EgressPolicy grants moved to the source definition
carry their grants in their pinned base, and keep them while an actor runs on
such a revision. An Agent whose `egressPolicyRefs` are set prepares a new
revision after the upgrade, since the references left revision identity: its
Sessions move to it at their next resume or the quiesced repoint, and from then
on policy edits reach them live. A sandbox stays on its revision for its whole
lifetime, at most the 24-hour maximum TTL. Persisted conversations, checkpoints
and Actors are unchanged by the upgrade; the database migration only adds the
grant columns to the source definitions, empty until the controller's first
preparation fills them.

## Authorization and scope

Policies are namespace-scoped configuration. Administrators can grant separate
RBAC permissions to author EgressPolicy objects; kagent's controller needs only
get/list/watch. Workload authors who can select a policy in that namespace can
use its grant. There is no separate per-policy `use` authorization check. Treat
the namespace as the sharing boundary for selectable policies and credentials.

The same trust-boundary consideration applies to existing ModelConfig and
RemoteMCPServer Secret references. This resource does not turn the namespace
into a per-workload Secret isolation boundary. Credential injection keeps values
out of compiled inputs; it does not prevent an allowed destination from echoing
credentials back to a client. Do not grant credentials to destinations that
should not receive or disclose them.

This API intentionally does not expose Substrate rule effects, raw TCP/SSH,
OAuth exchange, request signing, cross-namespace references,
or unrestricted public-internet access. Substrate remains the enforcement
backend; kagent exposes task-oriented reusable grants and static authentication.
