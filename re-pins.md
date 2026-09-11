# Re-pins

Every re-pin of the consumed branch onto upstream main, newest last (see FORK.md). A re-pin
that stopped (conflict, failed checks) is a pull request against the consumed branch, not a row.

| date (UTC) | previous pin | new pin | consumed head | replayed | dropped | run |
|---|---|---|---|---|---|---|
| 2026-09-10 20:51 | `3dcfc28` | `4a91c27` (2026-09-10) | `5dd243a` → `c231bd6` | 9 | `c0125c5` Use `apk upgrade` on harness images (#2756) — merged upstream as `8e1ff48` | [run](https://github.com/giantswarm/kagent-upstream/actions/runs/34526022137) — checks red on the first candidate (carried MCPServer patch vs upstream #2774), fixed and landed by hand |
| 2026-09-11 07:12 | `4a91c27` | `6a6b54f` (2026-09-10) | `7285528` → `ed07617` | 21 | — | [run](https://github.com/giantswarm/kagent-upstream/actions/runs/34571612774) |
