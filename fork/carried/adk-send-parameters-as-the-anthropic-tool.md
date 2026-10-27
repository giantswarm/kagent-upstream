---
id: adk-send-parameters-as-the-anthropic-tool
fork: kagent
theme: harness-variety
category: bugfix
status: carried
commits:
  - 'fix(adk): send Parameters as the Anthropic tool input schema'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3387, kagent-dev/kagent#3389]
  exit: '#3389 merges; the first re-pin onto a commit containing it drops this commit'
depends_on: []
verified: 2026-10-27
---
# fix(adk): send Parameters as the Anthropic tool input schema

## Change

`fix(adk): send Parameters as the Anthropic tool input schema`

## Reason

The Go ADK's Anthropic adapter (`genaiToolsToAnthropicTools`, `go/adk/pkg/models/anthropic_adk.go`) built a tool's `input_schema` from `FunctionDeclaration.ParametersJsonSchema` only. ADK's own tools (`transfer_to_agent`, `load_memory`, `load_artifacts`) declare their arguments in `Parameters`, a `genai.Schema`, so Claude saw them with an empty schema and called them without arguments. An agent with Shared subagents on an Anthropic ModelConfig never reached a child: every `transfer_to_agent` call failed with `empty agent_name: map[]`. The adapter now falls back to `Parameters` when `ParametersJsonSchema` is unset, as the OpenAI and Gemini adapters already read it.

## History

Filed upstream as kagent-dev/kagent#3387 with PR kagent-dev/kagent#3389 (2026-10-27). The carried commit is #3389's, cherry-picked with the test file merged beside the fork's non-streaming Anthropic tests.
