# omp Harness

The omp Harness runs [oh-my-pi](https://github.com/can1357/oh-my-pi) (omp)
`18.11.0` as a bring-your-own Harness. Kagent has no omp runtime, so a `Harness`
names the `omp-harness` image by digest with `workload.command` set, and the
controller compiles the Agent into the ADK-shaped `KAGENT_CONFIG_JSON` it gives
every BYO Harness. The image serves Kagent's private A2A contract, `/readyz` and
the TaskStore at `KAGENT_API_URL` on port 80 through the shared runtime
packages, and runs each turn as one `omp --mode rpc` process on the Actor's omp
session under `/data/omp`.

## Code structure

| Path | Look here for |
| --- | --- |
| [`cmd/main.go`](cmd/main.go) | Startup, environment inputs, omp version check, continuation store and private A2A |
| [`internal/config`](internal/config) | Reading the ADK configuration and refusing what the adapter does not implement |
| [`internal/adapter`](internal/adapter) | omp's files under `/data/omp`, the isolation flags and the MCP tool bridge |
| [`internal/driver`](internal/driver) | The omp process per turn, the RPC framing, event translation, resume and usage |

## Implemented support

- An Anthropic ModelConfig, the same one the claude Harness takes: the model is
  omp's only provider, `kagent/<model>`, on the ModelConfig's base URL (default
  `https://api.anthropic.com`) and headers. The key travels as the `X-Api-Key`
  header holding the `ANTHROPIC_API_KEY` placeholder the egress gateway
  replaces; omp sends no `Authorization`.
- Streamable HTTP MCP servers with their headers, Secret-backed headers as the
  egress gateway's placeholders. A server is named after the first label of its
  host (`mcp__muster_<tool>`). omp reaches every server through a loopback MCP
  bridge, the harness's MCP client towards the server: it holds the server's
  headers and timeout, serves omp the server's tools (only the selected ones
  when the binding selects tools, as omp has no MCP allowlist), resources,
  resource templates and prompts, and follows their list changes. A server on
  a host the turn names in `x-kagent-actor-jwt-hosts`, where the egress gateway
  hands the Session's Actor its own JWT, is sent the placeholder
  `Authorization: Bearer kagent-credential-injected` that the gateway
  replaces, unless the server sets its own `Authorization`.
- MCP URL mode elicitations, such as an MCP gateway's sign-in link. The bridge
  declares `elicitation.url`; a URL elicitation during a tool call (an
  `input_required` result on MCP 2026-07-28, `-32042` on 2025-11-25) parks the
  turn as `input-required` with a `url_elicitation_request` while the bridge
  holds omp's call open, and `url_elicitation_response` releases it: `accept`
  runs the call, `decline` and `cancel` end it without running. The binding's
  timeout does not run while the person decides, and omp's own MCP timeout is
  off. A form, a link that is not https, and an elicitation outside a tool call
  are declined. omp never sees the link.
- Standalone skills and the skills an Agent selects from Agent Plugins
  (`plugin.json` at the package root), written to omp's user skills directory
  by the shared materialiser; a plugin's unselected skills, `mcp.json` and
  other parts are not loaded.
- Shared subagent bindings (an AgentTemplate's `tools[].subAgent`), each an
  omp user task agent, `~/.omp/agent/agents/<binding>.md` under omp's home,
  that the root reaches through omp's `task` tool: the binding's name and
  description, the child's instruction as its prompt and its model as
  `kagent/<model>`. A child must use the root's ModelConfig settings but the
  model (base URL, headers, `maxTokens`), as on the claude and codex
  Harnesses. Its selected skills are installed with the root's and listed in
  the agent's `autoloadSkills`; the same skill name from two sources is
  refused. The child runs in the root's process, so the root sees its skills
  and it is given every MCP tool of the root.
- Streaming text, tool calls and results; one turn's usage (the difference of
  omp's session totals around the prompt, every prompt token counted as input
  and cache reads among them) on its final task status.
- Resume: a turn after a suspend starts omp with `--resume <session>` on the
  session directory under `/data`.

Refused with the reason: other model providers, API-key passthrough, custom
TLS, SSE and stdio MCP servers, tool approval, remote agents, a Shared child's
MCP tools (omp gives a subagent proxies of every MCP tool of its parent, and an
agent file's `tools` selects built-in tools only), nested Shared children, a
binding name omp reserves (`main`, `sub` and its bundled agents such as
`task`), memory, structured output and Claude-format plugins (`.claude-plugin/plugin.json`
without a root `plugin.json`): omp would load one only through its
`claude-plugins` provider, which also reads a workspace's
`.omp/plugins/installed_plugins.json` and runs no `hooks/hooks.json` hook.

## Isolation

A repository's own omp, `.claude`, `.codex` or other agent configuration adds
no tool, server, rule or provider:

- `--no-extensions` (ambient extensions, hooks and plugins) and `--no-rules`;
- a `--config` overlay, omp's highest layer below the flags, that turns off
  every discovery source but omp's own (`disabledProviders`), project MCP
  configuration and every skill source but the adapter's;
- the adapter rewrites the overlay, `models.yml`, `mcp.json`, the
  instruction and the Shared agents' files before every turn, so a turn cannot
  change the next turn's tools, providers or agents by editing them.

omp runs as the image's non-root user, the harness's own. The harness process
is not dumpable, so omp and its commands cannot read its environment or
memory; omp's environment carries neither the compiled configuration nor the
header variables of the servers, which only the bridges hold.

omp reads a workspace's `.omp/agents` before the user agents and keeps the
first agent of a name, and no setting turns that directory off: a repository
checked out at the workspace root can replace a Shared agent's definition.
Hooks have no off switch in omp beyond `--no-extensions`; the gap stays
documented until omp offers one. MCP tool selection keeps a server's other
tools away from the model, not from a shell command that guesses the gateway's
placeholder; the Substrate Actor and the egress gateway remain the boundary.

## The pin

The image installs omp's `omp-linux-musl-{x64,arm64}` release binary, checked
against the `OMP_*_SHA256` arguments of the [`Dockerfile`](Dockerfile).
`PinnedOmpVersion` in [`internal/config`](internal/config/config.go) is the only
version the harness accepts. Renovate moves both with every omp release;
`scripts/fork/harness-pins.sh` writes the checksums from the release's
`SHA256SUMS.txt`.

## Tests

`go test ./harness/omp/...` runs the unit tests. The integration tests run the
pinned omp against a fake Anthropic endpoint, an MCP server and a workspace
carrying hostile omp, Claude and Codex configuration, and against a server
that asks for a sign-in (the turn parked, accepted after omp's default MCP
timeout, declined, cancelled). The release binary is linked against musl;
on a glibc host run the test binary in an Alpine container with `libstdc++`:

```sh
KAGENT_OMP_BINARY=/path/to/omp-linux-x64 go test ./harness/omp/...
```
