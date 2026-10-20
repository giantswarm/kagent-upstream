---
id: a2agateway-no-caller-credential-to-a-runtime-that
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(a2agateway): no caller credential to a runtime that needs none'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3263]
  exit: "the platform binary's Authenticator.UpstreamAuth replaces the caller's Authorization with the placeholder (kagent-dev/kagent#3263): drops at the re-pin onto acc75416b together with migration 000007, whose record upstream's token_passthrough_targets and its every-target-bound check replace; re-check each re-pin"
depends_on: [harness-send-the-placeholder-to-actor-jwt-hosts, egress-actor-jwt-hosts-only-during-a-turn, kagent-dev/kagent#3263]
verified: 2026-10-22
---
# feat(a2agateway): no caller credential to a runtime that needs none

## Change

`feat(a2agateway): no caller credential to a runtime that needs none`

## Reason

in `trusted-proxy` mode the A2A gateway copied the caller's `Authorization` into every turn it sent to a Session's runtime, whatever the runtime did with it. On a Session whose MCP servers all sit on hosts where the egress gateway hands the Actor its own JWT, no runtime uses that bearer, yet every Harness process in the sandbox received the person's token, typically an IdP id_token other services accept too; only process isolation that was never designed for it kept a sandbox process from reading it. Each compiled revision now records where its runtime sends the caller's credential (`runtime_revision.caller_credential`, migration `000007`): the hosts of the MCP servers that take it under `KAGENT_PROPAGATE_TOKEN`, and whether an STS exchange or a ModelConfig with `apiKeyPassthrough` needs it whatever the hosts. The gateway reads the Session's prepared revision on dial and leaves `Authorization` off a turn unless a host lies outside the Session's actor-JWT hosts or such a use needs it; `X-User-Id`, the groups and `x-kagent-user` go either way. A revision recorded before the column has none and is handed the credential as before; the controller's next record of an Agent's current revision fills it in, the session repoint moves sessions onto it. The kagent Harness needs the Go ADK to send the placeholder to actor-JWT hosts first. `TestRuntimeDialerForwardsTheCallersCredentialOnlyWhereItIsUsed`, `TestCompileCallerCredential`, `TestCallerCredentialNeeded`, `TestRuntimeRevisionFillsInItsCallerCredentialOnce`

## History

to file — prepared in the fork once the Session's actor-JWT providers are upstream (kagent-dev/kagent#3156): the gateway's caller-credential decision rests on the Session's actor-JWT hosts, a fork-only input until then, so no upstream branch is prepared yet; filed with or after the Session caller-credential series.

2026-10-22, decided on internal issue 38652, comment 6340015449: class 3 platform; the need is real (the person's token reaches every sandbox process without it) and upstream has the seam: an embedder's `UpstreamAuth` swaps the caller token for the placeholder, and with compilers registered every passthrough target must be bound, so no runtime needs the person's token; the fork's per-revision `caller_credential` record and migration `000007` are not carried past that re-pin; until then installations get it from kagent v1.23.0; destination the platform's kagent-extensions binary (theme T9); first step its UpstreamAuth; test `TestRuntimeDialerForwardsTheCallersCredentialOnlyWhereItIsUsed` until the re-pin, then the lab's caller-credential proof; depends on themes T9 and T13 (the migration's conversion); owner Quentin.
