---
id: ci-pin-giantswarm-architect-orb-10-10-0
fork: kagent
theme: version-pins
category: pin
status: carried
commits:
  - 'ci: pin giantswarm/architect orb 10.10.0'
  - 'ci: pin giantswarm/architect orb 10.16.7'
upstream:
  outcome: fork-only
  refs: []
  exit: orb release
depends_on: []
verified: 2026-11-02
---
# ci: pin giantswarm/architect orb 10.10.0

## Change

`ci: pin giantswarm/architect orb 10.10.0`, then `ci: pin giantswarm/architect orb 10.16.7`

## Reason

the hand-written CircleCI config pins the orb release that carries giantswarm/architect-orb#952 and #956; the line has `gen.ci.generate: false`, so align-files does not carry the orb version here. 10.16.7 carries giantswarm/architect-orb#1006: `cosign-sign-verify` signs again when a Rekor duplicate-entry answer left the image unsigned in the registry, which failed `push-golang-adk` and `push-ui` on the v1.29.0 tag with "no signatures found"

## History

fork-only
