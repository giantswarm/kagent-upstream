---
id: substrate-the-egress-trust-bundle-carries-the-public
fork: kagent
theme: egress-policies
category: bugfix
status: carried
commits:
  - 'fix(substrate): the egress trust bundle carries the public roots'
upstream:
  outcome: to-file
  refs: [giantswarm/kagent-upstream#756]
  exit: upstream takes the tls:// kind with this trust file and a re-pin brings it; the line then drops this commit
depends_on: [api-tls-egresspolicy-destinations-as-tls-passthrough, api-tls-agent-spec-egress-destinations-as-tls-passthrough]
verified: 2026-10-20
---
# fix(substrate): the egress trust bundle carries the public roots

## Change

`fix(substrate): the egress trust bundle carries the public roots`

## Reason

an agent's default TLS clients could not verify a `tls://` (TLS passthrough) egress destination: the egress trust file every agent and sandbox Actor gets, `/run/kagent/egress/trust-bundle.pem` (`SSL_CERT_FILE`, `SSL_CERT_DIR` and the other CA variables), held the egress gateway's interception CA alone, so a passthrough destination's public chain had no anchor and the handshake failed with `certificate verify failed` although the policy allowed it. The agent and sandbox ActorTemplates now project Substrate's `egress-mitm.ate.dev` and `system-roots.ate.dev` (the public WebPKI roots Substrate ships) into that file, which Substrate unions into one PEM; the interception CA stays. The agent revision's `actorTemplateFormat` (4) and the sandbox inputs' version (4) move up so the changed templates render under new names, and sessions on superseded revisions are repointed. Persisted state keeps its format. `TestActorTemplateForRevision` and `TestSandboxPreparationPinsCompleteInputs` assert both names; `TestRevisionDigestChangesOnlyWithAnArtifactBinding` pins the format-4 digest

## History

to file — prepared in the fork: branch `upstream/egress-trust-system-roots`, stacked on `upstream/egress-policy-tls-passthrough-v2`, whose filing it waits on: upstream needs public roots only once a destination is passed through.
