---
id: crds-convert-the-lines-1-x-api-kagent-dev
fork: kagent
theme: compat-migrations
category: compat
status: carried
commits:
  - "feat(crds): convert the line's 1.x api.kagent.dev objects around the CRD upgrade"
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#3177]
  exit: 'leaves with the 2.x line once every installation runs it: no object of the 1.x shape is left'
depends_on: []
verified: 2026-11-26
---
# feat(crds): convert the line's 1.x api.kagent.dev objects around the CRD upgrade

## Change

`feat(crds): convert the line's 1.x api.kagent.dev objects around the CRD upgrade`

## Reason

kagent v1.0.1 keeps `api.kagent.dev/v1alpha3` and changes its shape: `Agent.spec.template` is a union (`inline` or `ref`), `templateRef` is gone, `harnessRef` is required and an inline harness is gone, `spec.egress` gave way to `EgressPolicy` objects in `egressPolicyRefs` (kagent-dev/kagent#3177), and `ModelConfig.spec.apiKeyPassthrough` is `tokenPassthrough`. With one stored version no conversion webhook can move an object, and once the new CRDs are installed the API server prunes the old fields on every read. The kagent-crds chart's `conversion` hooks save every object into Secrets before the CRD upgrade and write each object's new shape from that backup after it, failing on any field the installed CRDs would drop.

## History

2026-11-26: written for the re-pin onto kagent v1.0.1.
