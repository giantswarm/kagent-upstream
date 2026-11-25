---
id: a2a-run-on-a2a-go-2-7-0
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'fix(a2a): run on a2a-go 2.7.0, which refuses an A2A call without A2A-Version'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3474]
  exit: 'drops at the re-pin onto a commit containing df76aa3f, except the e2e connection interceptor, which stays while the line has e2e tests of its own that build raw A2A clients'
depends_on: []
verified: 2026-11-25
---
# fix(a2a): run on a2a-go 2.7.0, which refuses an A2A call without A2A-Version

## Change

`fix(a2a): run on a2a-go 2.7.0, which refuses an A2A call without A2A-Version`

## Reason

A2A 1.0 (section 3.6.2) treats a request without the `A2A-Version` header as a 0.3 request. a2a-go 2.6.0 served it as 1.0. 2.7.0 refuses it with `VersionNotSupported`, so a kagent that speaks only 1.0 no longer answers a 0.3 client in 1.0 shapes. Upstream took 2.7.0 in kagent-dev/kagent#3474 (`df76aa3f`, a grouped dependency bump), after the line's pin `92dd6caa`. The weekly re-pin is stopped and upstream `main` has since moved to a prerelease Substrate, so the line carries the A2A part of that commit:

- `go/go.mod`: `github.com/a2aproject/a2a-go/v2` v2.7.0, nothing else.
- `go/api/client`: `ListTaskPushConfigsPage` calls the generated gRPC client, which does not negotiate, so it sets the header itself. The `a2aclient`-based calls already send it.
- `ui/src/api/transport.ts`: an interceptor sets the header on every call to the A2A service, as upstream does.
- Tests: `TestCallWithoutA2AVersionIsRefused` (a gRPC and a JSON-RPC call without the header are refused) and `TestSessionAndA2AClientsUseTheirEndpoints` (every client call carries `1.0` once) fail on 2.6.0. The unit tests that build raw A2A requests send the header, as upstream's do.
- `go/core/test/e2e`: `newControllerConn` sets the header on calls to the A2A service, replacing a value a fixture set, instead of upstream's per-call contexts, because the line's own e2e tests build more raw A2A clients than upstream's (`TestWithA2AVersion`).

Callers outside kagent must send `A2A-Version: 1.0` before an installation runs this release: the Giant Swarm developer portal sends it from giantswarm/backstage#3647 (3.x) and its 2.97 backport, and agent-manager sends it on every call to the A2A service from v3.5.8 (giantswarm/agent-manager#480). An older agent-manager gets its first message of `start_session`, its turn list and a sent message refused with `FailedPrecondition` on a kagent with this change (giantswarm/agent-manager#479).

## History

2026-11-24: carried. Upstream merged the change in kagent-dev/kagent#3474; nothing to file.
2026-11-25: agent-manager named as a caller that must send `A2A-Version`; it does from v3.5.8.
