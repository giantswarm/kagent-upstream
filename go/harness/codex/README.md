# Codex Harness

The Codex Harness compiles an `api.kagent.dev/v1alpha3` `AgentTemplate` into a
compiler-owned Codex configuration and runs one Codex App Server `0.148.0`
process for each public A2A Task. Its native thread and workspace are retained
in the Actor's `DurableDir`.

## Code structure

The controller and Actor share only the versioned config contract. Kubernetes
resolution stays in the controller; Codex App Server behavior stays in this
harness.

| Path | Look here for |
| --- | --- |
| [`../../core/internal/translator/codex`](../../core/internal/translator/codex) | Translating `Harness`, `AgentTemplate`, model, MCP, plugin, and Secret inputs into a runtime revision |
| [`config/config.go`](config/config.go) | The versioned JSON contract shared by the compiler and runtime, including defaults and validation |
| [`cmd/main.go`](cmd/main.go) | Actor startup, environment inputs, Codex version validation, continuation-store wiring, and private A2A startup |
| [`internal/adapter`](internal/adapter) | Materializing `CODEX_HOME`, native TOML, shared agents, skills, and MCP configuration |
| [`internal/driver`](internal/driver) | App Server lifecycle, JSON-RPC framing, event translation, thread resume, cancellation, and process supervision |
| [`protocol/schema`](protocol/schema) | Generated App Server schema used as a development reference; regenerate it from the pinned CLI rather than hand-editing it |

## Implemented support

- OpenAI through a Secret-backed API key, the Responses API, and an optional
  absolute HTTP(S) base URL.
- A ChatGPT subscription login (`openAI.credentialType: chatgptOAuth`) against
  the ChatGPT Codex backend; the controller refreshes its access token in a
  Secret labelled `kagent.dev/chatgpt-login: "true"`. Without `apiKeySecret`
  it is the Session person's own login, which the egress gateway sets from
  the installation's credential-URI Session provider
  (`docs/architecture/credential-injection.md`).
- Amazon Bedrock through `AWS_BEARER_TOKEN_BEDROCK` in a Secret.
- Streaming text, command and file activity, direct Streamable HTTP MCP (with
  allowed-tools selection), and native Shared agents.
- Per-server MCP tool approval with live App Server pause, resume, and cancel.
- Native ask-user questions in default mode
- Standalone and plugin-selected skills without plugin hooks, commands,
  executables, or implicit plugin MCP servers.
- Exact native thread resume and bounded cancellation through `turn/interrupt`.

Credentials use [Substrate gateway injection](../../../docs/architecture/credential-injection.md).
AWS IAM keys are rejected because they cannot be injected as outbound HTTP
headers. Harness environment entries accept only literal values.

The adapter rewrites `config.toml` before every turn's App Server starts. A
server on a host the turn names in `x-kagent-actor-jwt-hosts`, where the egress
gateway hands the Session's Actor its own JWT, gets the placeholder
`Authorization: Bearer kagent-credential-injected` that the gateway replaces,
unless the server sets its own `Authorization`.

A turn whose dispatch names a working directory (`x-kagent-working-directory`)
runs Codex there, its configured workspace otherwise. A session directory on a
workspace volume is set up before its first turn as Codex's own user: a clone
with alternates of every mirror, the changed repositories fetched, and the
layout in `AGENTS.md`; what could not be cloned or fetched is the turn's first
status message. Codex's environment trusts the mirrors (`safe.directory`
through `GIT_CONFIG_COUNT`) and carries the placeholder credentials the egress
gateway replaces on a provider's host: git's credential helper, `GH_TOKEN` and,
in a workspace, glab's token. A `.codex/config.toml` in the turn's directory
is refused.

The adapter deliberately fixes the native sandbox to `danger-full-access`; the
Substrate Actor remains the security boundary. Its granular native approval
policy enables MCP elicitation and rejects command, sandbox, rule, skill, and
`request_permissions` prompts. MCP servers marked for approval prompt through
App Server elicitation, while other MCP servers remain automatically approved.
Account login, API-key passthrough, custom TLS, legacy SSE MCP, Dedicated agents,
checkpoint/fork guarantees, and configurable native policy are not advertised.

Runtime configuration is supplied through `KAGENT_CONFIG_JSON` and
`KAGENT_AGENT_CARD_JSON`. Private A2A is served on port 80 and readiness on
`/readyz` at port 8081.

## Human-in-the-loop flow

An MCP binding with `requireApproval: true` is emitted with Codex's default
tool approval mode set to `prompt`; other configured MCP servers use `approve`.
The driver exposes approval requests for the configured protected servers.
Although Codex transports these requests using the `mcpServer/elicitation/request`
App Server method, the external MCP server is not performing MCP elicitation;
an MCP server's own elicitation is described under
[MCP URL elicitation](#mcp-url-elicitation).

```mermaid
sequenceDiagram
    participant User
    participant Gateway
    participant Executor as A2A executor
    participant Driver as Codex driver
    participant Codex as Codex App Server
    participant MCP as Remote MCP server

    User->>Gateway: Send message
    Gateway->>Executor: Execute task
    Executor->>Driver: Run turn
    Driver->>Codex: turn/start
    Codex-->>Driver: MCP tool approval request
    Driver-->>Executor: Outcome{Pending: PendingTurn}
    Executor-->>Gateway: input-required
    Gateway-->>User: Show tool approval

    alt approve or reject
        User->>Gateway: Submit decision
        Gateway->>Executor: Continue task
        Executor->>Driver: PendingTurn.Resume(decision)
        Driver->>Codex: Reply to approval request
        opt approved
            Codex->>MCP: Call tool
            MCP-->>Codex: Tool result
        end
        Codex-->>Driver: More events or completion
    else cancel
        User->>Gateway: Cancel task
        Gateway->>Executor: Cancel
        Executor->>Driver: PendingTurn.Cancel
        Driver->>Codex: Cancel response and turn/interrupt
        Driver->>Driver: Reap App Server process
        Executor-->>Gateway: canceled
    end
```

## MCP URL elicitation

An MCP server can ask the person to open a URL outside the conversation, such
as a sign-in link. Codex relays that URL mode elicitation only on MCP
2026-07-28 (an `input_required` result of `tools/call`): on earlier revisions
it fails the call answered with `-32042`. The adapter therefore turns on the
pinned Codex's `mcp_2026_07_28` feature; a server that does not offer that
revision keeps the earlier one. Codex sends the elicitation as an
`mcpServer/elicitation/request` with `mode: "url"`, which the driver parks as
`input-required` and answers with the person's action (`accept` once the person
completed the sign-in, then Codex retries the call). Any other elicitation of
an MCP server is declined, since what the person typed into a form would reach
the model's context.

The feature is still under development in Codex, so the integration test runs
the pinned Codex against a fake Responses endpoint and an MCP server that asks
for a sign-in:

```sh
KAGENT_CODEX_BINARY=/path/to/codex go test ./harness/codex/internal/adapter/ -run TestCodexRelaysAURLElicitation
```

## Development

The Codex driver launches App Server and communicates with it using JSON-RPC
2.0. To refresh the reference schemas from the pinned Codex CLI, run:

```text
codex app-server generate-json-schema --out go/harness/codex/protocol/schema
```

For protocol details, see the [Codex App Server message
schema](https://learn.chatgpt.com/docs/app-server#message-schema).

## Example

One `Agent` holds both the template and Harness inline. The referenced ModelConfig
and RemoteMCPServer must already exist in `kagent`. Replace `${KAGENT_CODEX_IMAGE_DIGEST}`
with the full harness image reference, including its `@sha256:` digest.

```yaml
apiVersion: api.kagent.dev/v1alpha3
kind: Agent
metadata:
  name: kagent-codex
  namespace: kagent
spec:
  template:
    description: test
    modelConfig:
      name: default-model-config # This modelconfig must have openAI.apiFormat set to "responses"
    systemPrompt: |
        Follow the selected skill and use the configured MCP tool.
    tools:
      - mcp:
          server:
            kind: RemoteMCPServer
            name: kagent-tool-server
    plugins:
      - source:
          git:
            url: https://github.com/agentplugins/agent-plugins-example.git
            commit: 5f3f5084a821aefa792e79500dd8f0462ab83473
        skills:
          - migrate-agent-plugin
  harness:
    codex: {}
    workload:
      image: ${KAGENT_CODEX_IMAGE_DIGEST}
    substrate:
      workerPoolRef:
        name: kagent-default
      snapshotPolicy:
        location: gs://ate-snapshots/kagent/
```
