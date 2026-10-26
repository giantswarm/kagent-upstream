---
id: codex-carry-the-turns-error-message
fork: kagent
theme: credential-injection
category: bugfix
status: carried
commits:
  - 'fix(codex): carry the turn''s error message, and classify the credential refusal as connection_required'
upstream:
  outcome: open
  refs: [agentgateway/agentgateway#2162, kagent-dev/kagent#3377]
  exit: "the message half: kagent-dev/kagent#3377 merged, then it drops at the re-pin containing it; the connection_required half: an agentgateway/agentgateway issue for a machine-readable egress refusal reason, after which the classification and klaus-gateway match on that reason and the half drops; re-check each re-pin"
depends_on: [codex-a-persons-chatgpt-login-as, agentgateway/agentgateway#2162]
verified: 2026-10-26
---
# fix(codex): carry the turn's error message, and classify the credential refusal as connection_required

## Change

`fix(codex): carry the turn's error message, and classify the credential refusal as connection_required`

## Reason

a failed codex turn reported the fixed "Codex execution failed" and dropped Codex's `turn.error.message`, so a person without a ChatGPT connection, whose model request the egress gateway refuses with `credential provider <authority> denied: …`, saw no reason and no next step. The codex driver now carries the message (bounded, prefixed "Codex execution failed: ") and reports that refusal with `runtime.Failure.Reason` `connection_required`; the A2A runtime puts a failure's reason on the failed status message under `kagent.dev/a2a/failure-reason`, where a channel turns it into "connect your ChatGPT subscription". The refusal comes from the Session provider credentials this line carries, so the change stays fork-only until they are upstreamed.

## History

new with this change.

2026-10-22, decided on internal issue 38652, comment 6340015449: split: the message half is class 5 (a small pull request, uncapped), the classification half is class 4; agentgateway/agentgateway#2162 (merged 2026-10-21) makes every egress refusal body generic (`authorization failed`), so classifying the refusal by its text stops working at the agentgateway re-pin past be8864a88, and klaus-gateway's connection-required detection with it; the upstream ask is a machine-readable refusal reason on the response, never re-exposed error details; destination kagent-dev/kagent for the message, agentgateway/agentgateway for the reason; first step the agentgateway issue, and hold the agentgateway re-pin until the reason lands or a stopgap is decided; test the codex driver's error cases; depends on theme T6; owner Quentin.

2026-10-26: the message half filed as kagent-dev/kagent#3377 by fiunchinho from `upstream/codex-turn-error-message` on upstream `main` `e480daa3f` (+44/-1): the driver carries `turn.error.message` trimmed, capped at 400 bytes on a UTF-8 boundary and prefixed "Codex execution failed: ", which stays under the A2A executor's 512-byte failure limit; a turn without a message keeps the bare text; a table test covers both and a cut multi-byte character. The classification half, `runtime.Failure.Reason` and the A2A metadata, is not in it.
