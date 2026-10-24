---
id: skills-retry-a-git-fetch-on-a-transient-network
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(skills): retry a git fetch on a transient network failure'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#2646, kagent-dev/kagent#3099, kagent-dev/kagent#3258]
  exit: 'after #3099 merges: re-cut on its CloneGitCommit and file (LC_ALL=C on the git command; retry everything but 401, 403, 404 and an unknown ref)'
depends_on: []
verified: 2026-10-24
---
# fix(skills): retry a git fetch on a transient network failure

## Change

`fix(skills): retry a git fetch on a transient network failure`

## Reason

a golden boot fetched every git skill and plugin source once (`CloneGitCommit`: `git init`, `remote add`, one `git fetch --depth 1`), so one dropped connection on the sandbox's egress path crashed the whole boot and cost the controller's restart backoff (20 s, 40 s, …): the gvisor-lane e2e `TestGitArtifactCredentialDelivery/claude` met a 503 from the egress gateway on one boot and an unresolvable origin name on the next (giantswarm/kagent-upstream#271, #265). The fetch now runs up to four times, 1 s, 2 s and 4 s apart, while git fails on a transient network error (name resolution, a refused, timed-out or dropped connection, a 5xx answer); a 401, 403, 404 or unknown commit fails at once with git's error. The OCI source (crane's retry transport) and the S3 source (the AWS SDK's standard retryer) already retry and are unchanged

## History

to file — prepared in the fork: branch `upstream/skills-fetch-retry` (mirror `main` @ `92dd6caa` + the patch, `a6f70bba`, DCO-signed, no fork references, unit tests green there; its message is the pull-request text). kagent-dev/kagent searched 2026-10-12: nothing on a fetch retry; kagent-dev/kagent#3099 changes the same file for credentials and kagent-dev/kagent#2646 is the adjacent skills-init idempotency report

2026-10-24: held, not filed. kagent-dev/kagent#3099 (open, maintainer-reviewed, conflicting) rewrites `CloneGitCommit` with a context, a gateway-authorization flag and `GIT_CONFIG_*` plumbing, so a retry filed now sits until it lands and is rebased anyway; kagent-dev/kagent#3258 proposes deferring git and S3 sources. Notes for the re-cut: run git with `LC_ALL=C` (the transient match reads English git and libcurl text, a localized image silently loses the retry); invert the classification to retry every failure except a short fatal set (401, 403, 404, unknown ref, bad commit) instead of an open-ended transient list; keep four attempts and the 15 s budget so a restart stays inside a 30 s probe.
