---
id: egress-a-sessions-actor-jwt-on-its-bearer
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'feat(egress): a Session''s actor JWT on its Bearer providers'' hosts'
upstream:
  outcome: open
  refs: [agent-substrate/substrate#2567, agent-substrate/substrate#2541, kagent-dev/kagent#3441]
  exit: "with session-allow-the-admitted-provider-hosts (kagent-dev/kagent#3441); the path_prefix part follows giantswarm/substrate:egress-scopes-credential-header-to-request-path-prefix (2026-11-17); re-check each re-pin"
depends_on: [session-allow-the-admitted-provider-hosts, giantswarm/substrate:egress-scopes-credential-header-to-request-path-prefix]
verified: 2026-10-29
---
# feat(egress): a Session's actor JWT on its Bearer providers' hosts

## Change

`feat(egress): a Session's actor JWT on its Bearer providers' hosts`

## Reason

a Session started in a workspace works on the person's repositories with git and the provider's CLI, and needs the person's credential on exactly its providers' hosts, while a Session of the same Agent without a workspace gets none and the token never reaches the sandbox or the controller. Each `Bearer` provider of `Session.providers` now compiles into the Session Actor's own policy, on create, repoint and the move back after a lost race, as a `CredentialHeader` with `actor_jwt` bound to the provider's audience (agent-substrate/substrate#1960, in the line's Substrate 1.9.0): the egress gateway mints the Actor's JWT per request and the provider, or its broker, maps the subject to the Session's person. The template's default, the Agent's other Sessions and the Harness's own bindings are unchanged; a provider on a header the Agent already binds is refused, whatever its path prefix. A provider may narrow its host to a path prefix (`SessionProvider.path_prefix`, the grant's `pathPrefix`), compiled into `CredentialHeader.path_prefix` (giantswarm/substrate#344, in the line's Substrate 1.11.1): GitLab's Bearer API under `/api/` beside Basic git on the rest of the host; the admission refuses a malformed prefix and a host and prefix granted twice. A `Basic` provider's host gets no credential until the gateway's broker exchange (agent-substrate/substrate#1661, open) can inject the broker's token. The admission refuses a provider naming a broker outside `KAGENT_SESSION_ADMISSION_JWT_BROKERS` when set. `TestSessionEgressPolicyBindsTheProvidersCallerCredentials`, `TestSessionEgressPolicyRefusesAProviderOnACredentialedHeader`, `TestSessionEgressPolicyScopesAProviderToItsPathPrefix`, `TestRefusesAGrantNamingAnUnknownBroker`, `TestAdmitsAProviderPerPathPrefixOfAHost`

## History

to file — prepared in the fork: branch `upstream/egress-caller-credentials` (one DCO-signed commit on `upstream/session-admission-jwt`, the branch of kagent-dev/kagent#3156, adding `providers` with the provider hosts and the actor-JWT rules), filed with or after kagent-dev/kagent#3156; the path prefix stays fork-only until Substrate's `CredentialHeader.path_prefix` lands upstream; the end-to-end proof with a Bearer provider is giantswarm/agentlab#620 The upstream form is prepared as branch `upstream/session-egress-destinations-3` (`d8138ad5`, one DCO-signed commit on `upstream/session-volume-admission-2`, no fork references, a session header refused on a caller-token passthrough origin, `make -C go test` and lint green there), superseding the `upstream/egress-*` branches: `Session.egress` (field 20) in the vocabulary of an `EgressPolicy` destination, each header from a `credential_uri` under the installation's `sessions.admission.jwt.credentialAuthority` or an `actor_jwt`; the installation-wide actor-JWT providers become grant entries with `actor_jwt`, the path prefix waits for Substrate's `CredentialHeader.path_prefix` upstream. At the re-pin onto an upstream with it, the fork's `Session.providers` (field 18) ships its automatic migration to `egress`.

2026-10-20, decided on internal issue 38652, comment 6301541135: class 4 upstream issue for the Session part, class 6 fork-only for the path_prefix part; destination the same standalone kagent-dev/kagent issue, an actor_jwt entry per Session provider; first step that issue; branch `upstream/session-egress-destinations-3` @ `d8138ad50`; test `TestSessionEgressPolicyScopesAProviderToItsPathPrefix`; depends on theme T13; owner unassigned; draft: internal issue 38652, comment 6301542004.

2026-10-29: filed as kagent-dev/kagent#3441 by fiunchinho from the draft, with the `sessions.proto` reference refreshed to upstream `main` `d9cc3a826`: the compiler `Input` gains the Session, and optionally `Session.egress` in the `EgressPolicy` destination vocabulary, with a reference implementation offered; the outcome is open on its answer.
