---
id: build-retry-the-alpine-package-step-on-a
fork: kagent
theme: ci-publish-infra
category: infra
status: carried
commits:
  - 'fix(build): retry the Alpine package step on a transient mirror error'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3167]
  exit: 'kagent-dev/kagent#3167 merged, then the re-pin drops the Dockerfile loops; expected instead: fork-only fork infrastructure from 2026-11-17 with #3167 kept as the exit (acceptance unlikely); the .circleci/config.yml push-charts loop stays fork-only (upstream has no CircleCI)'
  class: 5
  destination: 'kagent-dev/kagent#3167 for the Dockerfile loops; the loop around apk add gettext in .circleci/config.yml push-charts is fork-only infrastructure'
  first_step: 'the author of kagent-dev/kagent#3167 rebases it onto main, folds in the go mod download loops and marks it ready for review'
  source: 'upstream/apk-retry@b9c718787 (+48/-18, conflicts with kagent-dev/kagent main in go/Dockerfile since #3159)'
  test: 'an apk stub that fails twice and then succeeds passes the build; a nonexistent package fails after three attempts'
  recheck: '2026-11-17, then each re-pin'
  owner: teemow
  draft: issuecomment-6301360144
  decision: issuecomment-6301358702
depends_on: [build-retry-go-mod-download-on-a-transient-proxy]
verified: 2026-10-20
---
# fix(build): retry the Alpine package step on a transient mirror error

## Change

`fix(build): retry the Alpine package step on a transient mirror error`

## Reason

an image build failed on one transient error from the Alpine mirror (`TLS: unspecified error` fetching the APKINDEX, then `unable to select packages`, in the `build (golang-adk, golang-adk)` job of a pull request that did not touch the image, giantswarm/kagent-upstream#280), turning its required checks red. Every apk step (`go/Dockerfile`, `go/harness/claude`, `go/harness/codex`, `go/sandbox/guest`, both stages of `ui/Dockerfile`) runs in one shared loop: three attempts, 5 s apart, each the whole upgrade and install; a missing package still fails after the third with apk's error and exit code. The image content is unchanged

## History

kagent-dev/kagent#3167, a draft pending the team's review before it is proposed (prepared as `upstream/apk-retry`); fork-only: the GitHub and GitLab CLI pins inside the loops of `go/Dockerfile` and `go/harness/claude`, and the same loop around `apk add gettext` in `.circleci/config.yml`'s `push-charts` job (upstream has no CircleCI pipeline)

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): upstream PR kagent-dev/kagent#3167 for the Dockerfiles, fork-only for the CircleCI loop. Inside the loops only the GitLab CLI pin of `go/harness/claude/Dockerfile` (line 46) is fork-only; the GitHub CLI is installed after the loop by `install-github-cli.sh` in both images.
