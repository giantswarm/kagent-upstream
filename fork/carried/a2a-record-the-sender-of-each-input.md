---
id: a2a-record-the-sender-of-each-input
fork: kagent
theme: writable-shares
category: feature
status: carried
commits:
  - 'feat(a2a): record the authenticated sender of each input'
upstream:
  outcome: to-file
  refs: []
  exit: 'Prepared in branch upstream/turn-sender-38074. Drop when upstream carries per-input authenticated sender attribution, or writable shares leave this fork.'
depends_on: []
verified: 2026-11-07
---
# Authenticated sender per input

## Reason

Shared conversations need to identify the person who supplied each input,
including a visitor whose share selects another person's session. A2A's role
field distinguishes user from agent but carries no authenticated user identity.
This generic metadata contract belongs at the authenticated gateway boundary.

The interaction service stamps the caller on each inbound message. Task history
and durable execution events retain this attribution across replies, retries,
replay and forks. The sender is independent of the session owner.

## History

Prepared for upstream as a per-input metadata contract without schema changes.
