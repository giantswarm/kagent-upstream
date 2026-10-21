---
id: a2agateway-no-caller-credential-to-a-runtime-that
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(a2agateway): no caller credential to a runtime that needs none'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3156]
  exit: with the Session caller-credential series
depends_on: [harness-send-the-placeholder-to-actor-jwt-hosts, egress-actor-jwt-hosts-only-during-a-turn]
verified: 2026-10-21
---
# feat(a2agateway): no caller credential to a runtime that needs none

## Change

`feat(a2agateway): no caller credential to a runtime that needs none`

## Reason

in `trusted-proxy` mode the A2A gateway copied the caller's `Authorization` into every turn it sent to a Session's runtime, whatever the runtime did with it. On a Session whose MCP servers all sit on hosts where the egress gateway hands the Actor its own JWT, no runtime uses that bearer, yet every Harness process in the sandbox received the person's token, typically an IdP id_token other services accept too; only process isolation that was never designed for it kept a sandbox process from reading it. Each compiled revision now records where its runtime sends the caller's credential (`runtime_revision.caller_credential`, migration `000007`): the hosts of the MCP servers that take it under `KAGENT_PROPAGATE_TOKEN`, and whether an STS exchange or a ModelConfig with `apiKeyPassthrough` needs it whatever the hosts. The gateway reads the Session's prepared revision on dial and leaves `Authorization` off a turn unless a host lies outside the Session's actor-JWT hosts or such a use needs it; `X-User-Id`, the groups and `x-kagent-user` go either way. A revision recorded before the column has none and is handed the credential as before; the controller's next record of an Agent's current revision fills it in, the session repoint moves sessions onto it. The kagent Harness needs the Go ADK to send the placeholder to actor-JWT hosts first. `TestRuntimeDialerForwardsTheCallersCredentialOnlyWhereItIsUsed`, `TestCompileCallerCredential`, `TestCallerCredentialNeeded`, `TestRuntimeRevisionFillsInItsCallerCredentialOnce`

## History

to file — prepared in the fork once the Session's actor-JWT providers are upstream (kagent-dev/kagent#3156): the gateway's caller-credential decision rests on the Session's actor-JWT hosts, a fork-only input until then, so no upstream branch is prepared yet; filed with or after the Session caller-credential series.
