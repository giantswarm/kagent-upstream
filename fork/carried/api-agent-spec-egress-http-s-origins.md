---
id: api-agent-spec-egress-http-s-origins
fork: kagent
theme: egress-policies
category: feature
status: dropped
commits:
  - 'feat(api): Agent.spec.egress — HTTP(S) origins an Agent may reach'
upstream:
  outcome: declined
  refs: [kagent-dev/kagent#3019, kagent-dev/kagent#3177]
  exit: retire at next re-pin; consumers move to EgressPolicy
depends_on: [api-add-reusable-egress-policies-for-agents]
verified: 2026-11-26
---
# feat(api): Agent.spec.egress — HTTP(S) origins an Agent may reach

## Change

`feat(api): Agent.spec.egress — HTTP(S) origins an Agent may reach`

## Reason

a coding agent must reach github.com, the installation's git hosts and its toolchain's registries, and no field added a destination: an actor's allowlist was only what its revision compiled. `Agent.spec.egress[]` lists HTTP(S) origins (`https://proxy.golang.org`, or a leftmost-label wildcard with at least two labels under it) the Agent may reach besides what its revision compiles; the compiler adds them to the revision's destinations and the policy shares the validation. On the Agent, not the Harness: the hosts belong to the workload and its environment, and a template stays portable. The Generic agent chart emits them per agent (giantswarm/agent#56)

## History

kagent-dev/kagent#3019 (`5d5ca194d`), closed upstream unmerged, carried as the pull request has it; replaced by kagent-dev/kagent#3177's `EgressPolicy` (`api-add-reusable-egress-policies-for-agents.md`), retired once the Generic agent chart renders an `EgressPolicy` per agent everywhere and nothing renders `spec.egress`

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), retired: kagent-dev/kagent#3177: Agent.spec.egressPolicyRefs (go/api/v1alpha3/agent_types.go:65) + EgressPolicy destinations; each Agent.spec.egress list becomes an EgressPolicy <agent>-egress by the kagent-crds conversion hooks (crds-convert-the-lines-1-x-api-kagent-dev). The next landed re-pin removes this file
