---
id: egress-the-actor-jwt-on-the-installations-providers
fork: kagent
theme: credential-injection
category: gs-specific
status: carried
commits:
  - 'feat(egress): the actor JWT on the installation''s providers that verify it'
  - 'fix(session): the actor JWT providers on a Session created without a grant'
  - 'test(e2e): a Session without a grant reaches the actor JWT provider as its Actor'
  - 'fix(session): actor JWT providers only on a Session its creator binds'
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3263, kagent-dev/kagent#3093]
  exit: "the platform's egress policy compiler on kagent-dev/kagent#3263's EgressPolicyCompilers carries the installation's actor-JWT entries and is proven in the lab: drops at that re-pin together with bind_actor and the grant-less commits; re-check each re-pin"
depends_on: [kagent-dev/kagent#3263]
verified: 2026-10-20
---
# feat(egress): the actor JWT on the installation's providers that verify it

## Change

`feat(egress): the actor JWT on the installation's providers that verify it`

## Reason

a workspace Session's sandbox calls muster as its person with no credential and no loopback token in the sandbox: muster verifies Substrate's actor JWT itself and serves the person the Session's actor is bound to with the reserved provider `muster`. The installation names such provider instances with their hostname and audience (`KAGENT_SESSION_ACTOR_JWT_PROVIDERS`, chart `controller.session.actorJWTProviders`); every Session admitted with a grant gets each as a `Bearer` provider on its host, stored with the grant's own providers (so the binding agent-manager makes from `Session.providers` lists it), compiled on create, repoint and the move back after a lost race into a `CredentialHeader` with `actor_jwt` bound to the instance's audience (agent-substrate/substrate#1960) instead of a credential URI, on every origin of the host the Actor's policy allows. It needs no credential authority; a grant naming the instance or a provider on its host on any path is refused. A Session created without a volume source (a chat thread started outside a workspace) gets the same providers alone when its creator binds its Actor (`CreateSessionRequest.bind_actor`, which agent-manager's `start_session` sets), so its person binds it in muster as a workspace one and a Harness without a caller forwarder (codex) reaches muster's tools; one nobody binds (a Session created straight through the API) lists none and its runtime keeps forwarding the caller's credential, since muster refuses an actor nobody bound. `TestSessionEgressPolicyBindsTheActorJWTOfAProviderThatVerifiesIt`, `TestAdmitsEverySessionWithTheActorJWTProviders`, `TestParseActorJWTProviders`, `TestServiceCreateGivesASessionWithoutAVolumeTheActorJWTProviders`, the chart test for the value

## History

The grant-less Session's providers: to file — prepared in the fork: branch `upstream/actor-jwt-providers-without-grant` (one commit on `upstream/egress-actor-jwt-providers`).
to file — prepared in the fork: branch `upstream/egress-actor-jwt-providers` (one DCO-signed commit on `upstream/egress-provider-credential-uri`), filed with or after kagent-dev/kagent#3156 The upstream form is prepared as branch `upstream/session-egress-destinations-3` (`d8138ad5`, one DCO-signed commit on `upstream/session-volume-admission-2`, no fork references, a session header refused on a caller-token passthrough origin, `make -C go test` and lint green there), superseding the `upstream/egress-*` branches: `Session.egress` (field 20) in the vocabulary of an `EgressPolicy` destination, each header from a `credential_uri` under the installation's `sessions.admission.jwt.credentialAuthority` or an `actor_jwt`; the installation-wide actor-JWT providers become grant entries with `actor_jwt`, the path prefix waits for Substrate's `CredentialHeader.path_prefix` upstream. At the re-pin onto an upstream with it, the fork's `Session.providers` (field 18) ships its automatic migration to `egress`.

2026-10-20, decided on internal issue 38652, comment 6301541135: class 3 platform; destination a Compiler in the platform's kagent-extensions binary (theme T9) registered through `EgressPolicyCompilers` (kagent-dev/kagent#3263, `go/core/pkg/egresspolicy/egresspolicy.go:18-28`), which injects for every actor since its Input has only Atespace and GatewayTargets; invariant: every provider on the installation's list refuses an unbound actor (muster verified, `ErrActorNotAllowed`), checked before a provider joins; first step the Compiler, new branch in kagent-extensions; optional upstream follow-up a comment on kagent-dev/kagent#3093 (an actor-JWT value source on EgressPolicy); test `TestSessionEgressPolicyBindsTheActorJWTOfAProviderThatVerifiesIt` and the lab's caller-credential proof; depends on themes T9 and T13 (until it moves, the fork's egress compile is re-seated on #3263's chain at every re-pin); owner unassigned; draft: internal issue 38652, comment 6301543302.
