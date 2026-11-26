---
id: deps-update-opentelemetry-go-to-v1-47-0
fork: kagent
theme: version-pins
category: pin
status: dropped
commits:
  - 'fix(deps): update OpenTelemetry Go to v1.47.0'
upstream:
  outcome: merged
  refs: [kagent-dev/kagent#3229]
  exit: Drop the pin at the next re-pin that includes kagent-dev/kagent#3229 (a9b39e01f), which moves upstream to OpenTelemetry Go v1.47.0.
depends_on: []
verified: 2026-11-26
---
# Update OpenTelemetry Go to v1.47.0

## Change

Update the OpenTelemetry Go modules from v1.46.0 to v1.47.0 (log modules to their first stable v1.47.0, prometheus exporter v0.69.0, log exporters v0.23.0), and replace the deprecated `otel/log/global` calls with `otel.SetLoggerProvider` and `otel.GetLoggerProvider`, as upstream does.

## Reason

govulncheck reports GO-2026-6974 (attribute value length limit bypass in `otel/sdk`) and GO-2026-6975 (memory amplification in W3C baggage parsing in `otel`), both reached from kagent code and fixed in v1.47.0. The consumed upstream pin predates kagent-dev/kagent#3229, which made the same update.

## History

2026-11-24: verified kagent-dev/kagent#3229 merged on 2026-10-15 with v1.47.0 and `otel.SetLoggerProvider`.

dropped at the re-pin onto kagent v1.0.1 (`release/v1.0.x@558e3d7`), upstream provides it: go/go.mod:62 go.opentelemetry.io/otel v1.47.0. The next landed re-pin removes this file
