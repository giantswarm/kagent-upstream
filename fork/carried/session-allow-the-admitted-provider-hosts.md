---
id: session-allow-the-admitted-provider-hosts
fork: kagent
theme: credential-injection
category: feature
status: carried
commits:
  - 'fix(session): allow the admitted provider hosts on the Session''s own Actor'
upstream:
  outcome: to-file
  refs: [agent-substrate/substrate#2567]
  exit: "a standalone kagent-dev/kagent issue on Session-scoped egress destinations, filed by 2026-11-17 once agent-substrate/substrate#2567's direction is known; upstream ships Session-scoped egress: re-pin; declined: fork-only; re-check each re-pin"
depends_on: [agent-substrate/substrate#2567]
verified: 2026-10-20
---
# fix(session): allow the admitted provider hosts on the Session's own Actor

## Change

`fix(session): allow the admitted provider hosts on the Session's own Actor`

## Reason

a Session on a workspace volume sets up its directory before the first turn by fetching the repositories that moved since the last sync from its providers, and then works there with git and the provider's CLI, all through the egress gateway. Its Actor's policy was compiled from the revision alone, so an Agent whose `spec.egress` did not list the provider's origin had every fetch refused with a 403 and worked on the last sync, and an author cannot know which provider a person's workspace uses. Each admitted provider's host (`Session.providers`) is now an HTTPS origin of the Session's own policy on create, on a repoint and on the move back after a lost race; the template's default the Actor starts with, the revision and the Agent's other Sessions are unchanged. `TestSessionEgressPolicyAllowsTheSessionsProviders`

## History

to file with the Session's `providers`: the upstream form of the signed-grant admission (kagent-dev/kagent#3156) leaves `providers` out until the per-Session caller credentials are opened upstream; this commit goes with that filing The upstream form is prepared as branch `upstream/session-egress-destinations-3` (`d8138ad5`, one DCO-signed commit on `upstream/session-volume-admission-2`, no fork references, a session header refused on a caller-token passthrough origin, `make -C go test` and lint green there), superseding the `upstream/egress-*` branches: `Session.egress` (field 20) in the vocabulary of an `EgressPolicy` destination, each header from a `credential_uri` under the installation's `sessions.admission.jwt.credentialAuthority` or an `actor_jwt`; the installation-wide actor-JWT providers become grant entries with `actor_jwt`, the path prefix waits for Substrate's `CredentialHeader.path_prefix` upstream. At the re-pin onto an upstream with it, the fork's `Session.providers` (field 18) ships its automatic migration to `egress`.

2026-10-20, decided on internal issue 38652, comment 6301541135: class 4 upstream issue; destination a standalone kagent-dev/kagent issue, Session-scoped egress destinations and credential entries (the compiler Input of #3263 has no Session); first step that issue, kagent-dev/kagent, by 2026-11-17; branch `upstream/session-egress-destinations-3` @ `d8138ad50` as the reference shape; test `TestSessionEgressPolicyAllowsTheSessionsProviders`; depends on themes T2 and T13 (the `Session.providers` field 18 to 20 migration rides T13's conversion); owner unassigned; draft: internal issue 38652, comment 6301542004.
