---
id: ci-fork-publish-the-controller-with-giant-swarms
fork: kagent
theme: identity-authorizer
category: gs-specific
status: carried
commits:
  - "ci(fork): publish the controller with Giant Swarm's authenticator and authorizer as controller-extensions"
upstream:
  outcome: fork-only
  refs: [kagent-dev/kagent#2658, kagent-dev/kagent#3326]
  exit: 'the platform runs the stock controller image, its authorization and egress policy compilation answered by webhooks (kagent-dev/kagent#3326); the image is then no longer consumed and this commit drops at the following re-pin. Re-check: 2026-11-17'
depends_on: []
verified: 2026-10-22
---
# ci(fork): publish the controller with Giant Swarm's authenticator and authorizer as controller-extensions

## Change

`ci(fork): publish the controller with Giant Swarm's authenticator and authorizer as controller-extensions`

## Reason

the platform's controller is giantswarm/kagent-extensions: the kagent controller embedded through upstream's `app.Options` with Giant Swarm's authenticator and authorizer. A consumer runs it under the kagent chart, which is written for the controller of its own release: the CRDs, the configuration and the database schema the controller migrates at start. Built and released in its own repository, the binary pins one release of this line while the chart follows the installation's range, and nothing keeps the two on one release. The line therefore publishes it beside the controller, from the same commit: `go/extensions.Dockerfile` builds the kagent-extensions source at the commit `go/kagent-extensions.commit` pins (moved by Renovate's regex manager) against this tree, with this tree's replace lines and `FORK_VERSION` set to the build's version, and the pipeline publishes it as `giantswarm/kagent/controller-extensions` with the line's version, multi-arch, cosign-signed and attested, its digest the last column of `builds.md`. A consumer sets the controller's image repository to it, and the chart's own tag names the same release. The binary refuses to start under a chart of another release (its `FORK_VERSION` against the chart's `IMAGE_TAG`)

## History

fork-only, never upstream: it builds a Giant Swarm component. Its exit is the webhook proposal kagent-dev/kagent#3326 (filed 2026-10-22).
