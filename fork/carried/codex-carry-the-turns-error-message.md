---
id: codex-carry-the-turns-error-message
fork: kagent
theme: credential-injection
category: fix
status: carried
commits:
  - 'fix(codex): carry the turn''s error message, and classify the credential refusal as connection_required'
upstream:
  outcome: fork-only
  refs: []
  exit: with the upstreaming of the Session provider credentials whose refusal it classifies
depends_on: [codex-a-persons-chatgpt-login-as]
verified: 2026-10-22
---
# fix(codex): carry the turn's error message, and classify the credential refusal as connection_required

## Change

`fix(codex): carry the turn's error message, and classify the credential refusal as connection_required`

## Reason

a failed codex turn reported the fixed "Codex execution failed" and dropped Codex's `turn.error.message`, so a person without a ChatGPT connection, whose model request the egress gateway refuses with `credential provider <authority> denied: …`, saw no reason and no next step. The codex driver now carries the message (bounded, prefixed "Codex execution failed: ") and reports that refusal with `runtime.Failure.Reason` `connection_required`; the A2A runtime puts a failure's reason on the failed status message under `kagent.dev/a2a/failure-reason`, where a channel turns it into "connect your ChatGPT subscription". The refusal comes from the Session provider credentials this line carries, so the change stays fork-only until they are upstreamed.

## History

new with this change.
