---
id: api-add-reusable-egress-policies-for-agents
fork: kagent
theme: egress-policies
category: feature
status: carried
commits:
  - 'feat(api): add reusable egress policies for agents and sandboxes'
  - 'test(controller): install the EgressPolicy CRD in the sandbox envtest'
  - 'fix(api): regenerate the EgressPolicy CRD and gofmt the sandbox collections'
  - 'feat(egress): EgressPolicy changes reach running and suspended actors'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3177]
  exit: re-pin takes upstream's keying (by origin); fork port keyed by hostname+header
depends_on: []
verified: 2026-10-19
---
# feat(api): add reusable egress policies for agents and sandboxes

## Change

`feat(api): add reusable egress policies for agents and sandboxes`, `test(controller): install the EgressPolicy CRD in the sandbox envtest`, `fix(api): regenerate the EgressPolicy CRD and gofmt the sandbox collections` and `feat(egress): EgressPolicy changes reach running and suspended actors`

## Reason

the platform gives a tenant its own HTTP(S) grants and credentials without editing the Agent's template or Harness: `EgressPolicy` (`api.kagent.dev/v1alpha3`, namespaced, `helm/kagent-crds`) lists up to 64 origins, each optionally with a header, prefix and same-namespace `secretKeyRef`; `Agent.spec.egressPolicyRefs` and `SandboxTemplate.spec.egressPolicyRefs` select up to 16 policies in the workload's own namespace. As upstream merged it, grants are live, not pinned: the translator's `CompileEgressPolicies` resolves each policy on its own through KRT (a missing or deleting policy, a missing Secret or key, or an invalid policy contributes no grant), the Agent and SandboxTemplate reconcilers store the admitted union on the source definition (`agent_definition` and `sandbox_template_definition` gain `grant_destinations` and `grant_credentials`, core migration `000008`), and every reconcile applies the effective policy, the revision's pinned base plus the grants that fit it (`egress.FilterGrants`), to every running and suspended actor of the source through a version-fenced Substrate update; Session create, resume and repoint and sandbox create and resume read the same live grants. Grants and references leave revision identity, so a policy edit prepares no revision and restarts nothing; an independent `EgressPoliciesApplied` condition (`Applied`, `EgressPolicyNotFound`, `InvalidEgressPolicy`, `EgressGrantConflict`) reports admission and never holds `Ready`; a conflict with the pinned base holds additions and still applies removals; a deleted Agent's or SandboxTemplate's actors lose their grants, after a controller restart too. Secret values never enter compiled inputs. Revisions prepared before the change keep the grants they pinned until their Sessions move to a post-change revision (an Agent with references prepares one, its Sessions repoint at resume or the quiesced sweep) or, for a sandbox, until it expires. The controller gets get/list/watch on `egresspolicies`. Composes with the line's egress: `Agent.spec.egress` origins, the actor JWT on a Session's Bearer providers, golden-scoped artifact credentials and the template's default policy are unchanged. Ported onto the line's credential model: bindings stay keyed by hostname and header (upstream's pull request re-keys them by origin), so no persisted revision changes format; upstream's `fix: require current prepared revisions for new instances`, which refuses new Sessions while any revision prepares, is not carried. `TestEgressPolicyGrantsDestinationAndCredential`, `TestEgressPolicyResolvesInTheAgentsNamespace`

## History

kagent-dev/kagent#3177 — merged 2026-10-17 (`a0ad6cb9`; `3a8bb732`…`5d6a8d16` ported here, `22473336` left out), carried until the re-pin onto a commit containing `a0ad6cb9` takes upstream's form
