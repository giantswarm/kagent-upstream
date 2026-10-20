---
id: docker-give-the-go-runtime-user-a-writable-home
fork: kagent
theme: ci-publish-infra
category: bugfix
status: carried
commits:
  - 'fix(docker): give the Go runtime user a writable HOME'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3161]
  exit: 'drops at the re-pin onto a commit containing e80e7ab1'
  class: 2
  destination: 'kagent-dev/kagent#3161, merged 2026-10-20 as e80e7ab1'
  first_step: 'leave the commit out at the first re-pin onto a commit containing e80e7ab1'
  test: 'the re-pinned golang-adk image: docker run --rm --entrypoint sh <image> -c ''test "$HOME" = /home/kagent && git config --global user.name x'' passes'
  recheck: 'the next re-pin'
  owner: unassigned
  decision: issuecomment-6301358702
depends_on: []
verified: 2026-10-20
---
# fix(docker): give the Go runtime user a writable HOME

## Change

`fix(docker): give the Go runtime user a writable HOME`

## Reason

the Go runtime image ran uid 65532 without a passwd entry, so `HOME` was `/`, owned by root, and the Go ADK hands the runtime's `HOME` to every command the model runs: `git config --global` and CLIs that create `$HOME/.config` (`glab`) failed with `mkdir /.config: permission denied` (giantswarm/kagent-upstream#429). The image gives uid 65532 a passwd entry with `/home/kagent`, owned by it, and sets `HOME` there, since Substrate takes `HOME` from the image's `ENV`, not from passwd

## History

kagent-dev/kagent#3161

2026-10-20: placed by the T16 ci-publish-infra theme review (the decision comment is `upstream.decision` on the theme issue): kagent-dev/kagent#3161 merged on 2026-10-20 (`e80e7ab1`), after `96c40300`.
