---
id: agentplugins-fetch-a-moved-plugin-pin-again
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(agentplugins): fetch a moved plugin pin again instead of reusing the previous package'
  - 'fix(agentplugins): key package caches by artifact and fetch each artifact once'
  - 'fix(agentplugins): try an artifact whose fetch failed once per start'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3503]
  exit: kagent-dev/kagent#3503 merges; the re-pin drops the first two commits, and the third is re-cut on it and filed
depends_on: []
verified: 2026-11-25
---
# fix(agentplugins): fetch a moved plugin pin again instead of reusing the previous package

## Change

`fix(agentplugins): fetch a moved plugin pin again instead of reusing the previous package`
`fix(agentplugins): key package caches by artifact and fetch each artifact once`
`fix(agentplugins): try an artifact whose fetch failed once per start`

## Reason

The shared materializer reused any package directory it found under `Paths.Packages` if it held the required file, without checking which source had filled it. The codex Harness keeps its packages under `<DurableDir>/codex/packages`, the claude Harness running as a non-root user under `<DurableDir>/generated/packages`, and the omp Harness under `<DurableDir>/omp/packages`. None of them reset that directory, so a start whose skill or plugin pin moved ran the previous commit's skills, even when the new commit was reachable. Upstream `main` has the same code. `fetchSource` now records the source it fetched next to the package directory (`<package>.source`) and reuses the directory only for that exact source; any other directory is fetched again. The tests that built package directories by hand now fetch from local git repositories.

The second commit ports the rest of kagent-dev/kagent#3503: a package's cache entry is named by a hash of its artifact (the source without `path`), fetched into a staging directory and published by rename after validation, and entries no start uses any more, including the old `standalone-N` and `plugin-N` directories, are pruned. Sources that differ only in `path` share one entry and one fetch: an Agent taking ten skills from one repository and commit fetched it ten times, about 3 to 4s each in a 1 to 2 CPU gVisor sandbox, and missed the 30s wakeup probe; on 1.33.0, which sizes the sandbox from the Harness, Agents with ten such skills stopped starting. OCI sources, authenticated Git and the durable copies stay as this fork has them.

The third commit keeps a failed fetch from being repeated for every source of the same artifact within one start. The cache entry is published only after a successful fetch, so when an artifact cannot be fetched, each of its sources missed the entry and fetched again, each time spending the git fetch's retry budget of up to 15s on a transient network error before falling back to its durable copy: during a git outage an Agent with ten skills from one commit could spend up to about 150s this way, far past the 30s wakeup probe, although every skill had a durable copy. The first error is now returned to the artifact's other sources, which fall back to their durable copies or fail with it. kagent-dev/kagent#3503 has the same repetition without the retry budget.

## History

to file; filed upstream as kagent-dev/kagent#3503 on 2026-11-03
2026-11-24: cache keyed by artifact and one fetch per artifact ported from #3503's head (6736528d5), after Agents with ten skills from one commit stopped starting on 1.33.0
2026-11-25: a failed artifact fetch is tried once per start, not once per source
