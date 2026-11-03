---
id: agentplugins-fetch-a-moved-plugin-pin-again
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(agentplugins): fetch a moved plugin pin again instead of reusing the previous package'
upstream:
  outcome: to-file
  refs: []
  exit: upstream merges the source record; the re-pin drops the commit
depends_on: []
verified: 2026-11-03
---
# fix(agentplugins): fetch a moved plugin pin again instead of reusing the previous package

## Change

`fix(agentplugins): fetch a moved plugin pin again instead of reusing the previous package`

## Reason

The shared materializer reused any package directory it found under `Paths.Packages` if it held the required file, without checking which source had filled it. The codex Harness keeps its packages under `<DurableDir>/codex/packages`, the claude Harness running as a non-root user under `<DurableDir>/generated/packages`, and the omp Harness under `<DurableDir>/omp/packages`. None of them reset that directory, so a start whose skill or plugin pin moved ran the previous commit's skills, even when the new commit was reachable. Upstream `main` has the same code. `fetchSource` now records the source it fetched next to the package directory (`<package>.source`) and reuses the directory only for that exact source; any other directory is fetched again. The tests that built package directories by hand now fetch from local git repositories.

## History

to file
