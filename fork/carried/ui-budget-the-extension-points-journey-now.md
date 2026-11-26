---
id: ui-budget-the-extension-points-journey-now
fork: kagent
theme: test-flakes
category: test
status: dropped
commits:
  - 'test(ui): budget the extension points journey now that its file holds a second test'
upstream:
  outcome: fork-only
  refs: []
  exit: drop as runner-specific
depends_on: [ui-count-a-journeys-steps-per-test]
verified: 2026-11-26
---
# test(ui): budget the extension points journey now that its file holds a second test

## Change

`test(ui): budget the extension points journey now that its file holds a second test`

## Reason

upstream added a second test to `extension-points.withExtension.spec.ts`, so its eleven-step journey no longer carried the file's lifecycle budget and the layout convention of `test(ui): count a journey's steps per test` failed on every run; the journey sets the budget in its body

## History

fork-only: the convention is this line's

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), retired: n/a (runner-specific test budget). The next landed re-pin removes this file
