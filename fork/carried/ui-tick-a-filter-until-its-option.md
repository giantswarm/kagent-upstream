---
id: ui-tick-a-filter-until-its-option
fork: kagent
theme: test-flakes
category: test
status: carried
commits:
  - 'chore(ui): tick a filter until its option confirms it'
upstream:
  outcome: to-file
  refs: [kagent-dev/kagent#3392]
  exit: file in the UI test batch; drops at the re-pin that takes an upstream equivalent of chooseFilter's confirmed retry
depends_on: []
verified: 2026-11-08
---
# chore(ui): tick a filter until its option confirms it

## Change

`chore(ui): tick a filter until its option confirms it`

## Reason

the fork's `ui-tests` reported "agents: selecting no namespace means every namespace, and a pill undoes one" flaky on both engines (giantswarm/kagent-upstream#237, giantswarm/kagent-upstream#524): four of 59 CI runs between 2026-10-27 and 2026-11-07 retried it, every time at step 2 with no `agents-filters-pill-ns-analytics` pill. The failed attempt's snapshot shows the filter still on "All namespaces", the popup closed and all nine rows: the click on the `analytics` option, still sliding in, selected nothing, and Escape closed the popup. `chooseFilter` now opens the popup only when the option is not on screen, presses the option with `pressOnce` only while its `aria-selected` is not the wanted state (an option toggles, so a press that landed late is never undone), and confirms that state, retried for half the test budget; `{ ticked: false }` unticks. The agents spec and the prompts spec's untick of `platform` use it in place of their own unconfirmed clicks; the agents spec's two pill presses (the namespace pill, "clear filters") and the prompts spec's "Try again" are pressed with `pressUntil` until the page has acted, where one lost press failed the step

## History

to file — prepared in the fork: branch `upstream/ui-tick-a-filter` (on upstream `main`, DCO-signed, no fork references). kagent-dev/kagent#3392 (merged 2026-10-28, `1b9b1196`, after the pin) fixed the agents spec's press with `pressOnce` and a pill check before Escape; this change goes further, with one confirmed, retried press in the shared helper for every filter and the agents and prompts specs' other unconfirmed presses
