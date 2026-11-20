# CLI credential integration audit

Inspected 2026-11-20 at fork `b7511e165` and upstream `d5c04c178`.
Environment values are configuration. A Session grant and the gateway's live
policy authorize credential use. Copying or changing a placeholder grants
nothing. Real provider credentials belong only at the gateway/provider boundary.

## Ownership

| Feature | Classification | Mechanism and migration gate |
| --- | --- | --- |
| Literal Harness environment; Agent selects Harness | Already upstream | `Harness.spec.env`, literal `RuntimeEnvVar`; no Session override is required. |
| Model and MCP Secret bindings | Already upstream | Compiler binds destination/header to a gateway credential URI. Keep these bindings. |
| Generic embedded-controller authentication and egress compilation | Already upstream | `Authenticator.UpstreamAuth`, `EgressPolicyCompilers`, token passthrough, merged upstream #3257 and #3263. A platform binary must actually supply the replacements before fork adapters drop. |
| ADK shell isolation and declared shell values | Generic upstream candidate | #3002 open; #3014 adopted an allowlist but closed stale, unmerged. Serialize exact Harness values and overlay after filtering, including shared agents and setup. Local proposal prepared. |
| Session provider destinations and credential URIs | Generic upstream candidate | #3441 remains open. Session-aware compilation must preserve grant scope, authority validation, conflict checks, create/resume/repoint/rollback behavior. Keep current implementation until proven equivalent. |
| Actor-JWT credential lifetime bounded by the turn | Generic upstream candidate | #3440 remains open. Installation compilation alone does not replace per-turn opening and fenced closing. |
| Private git artifacts and golden-only credentials | Generic upstream candidate | #3099 remains open; current upstream lacks the fork's `ScopeGolden` mechanism. Keep source authentication separate from Session credentials and retain golden-only filtering. |
| Turn git author/committer identity | Generic upstream candidate | The authenticated turn identifies the person; it grants no authentication. Evaluate independently from forge environment settings. |
| Forge placeholder values and git credential helper | Platform-owned | The platform declares literal Harness values; the runtimes generate no forge credentials or git credential helper. |
| glab OAuth mode and host configuration | Platform-owned | The platform declares OAuth Bearer mode and the configured GitLab host. Multiple hosts require explicit Harness selection. The runtime writes no forge configuration. |
| Installation identity-to-provider mapping and grant issuer | Platform-owned | workspace-manager and provider plugins authenticate the person and authorize the Session. kagent should consume generic grants and credential URIs. |
| Installation actor-JWT services | Platform-owned | Bind only services that verify the Actor identity. Moving CLI setup does not remove those bindings. |
| Forge binaries and versions | Platform-owned | Digest-pinned github/gitlab toolchains, independently of Harness images. Current fork images carry git but no gh/glab. |
| Toolchain runtime compatibility | Platform-owned | Compatibility labels name Harness runtime kinds. OMP uses `byo`; agent-toolchains#44 corrects the labels and verifies all five toolchains against the pinned OMP image. Consume compatible artifacts before validating OMP Sessions. |
| Native Claude/Codex/OMP MCP placeholder adapters | Mixed | Generic token-passthrough support is upstream; platform mappings and OMP adapter ownership remain separate. A CLI environment migration cannot remove native MCP adapters. |
| No caller bearer when all targets use gateway identity | Generic upstream mechanism plus platform integration | Upstream UpstreamAuth/token-passthrough targets support this; keep the fork's protection until every target is bound and the deployed platform supplies the authenticator. |
| Path-scoped credentials, header replacement and provider fetch | Gateway/provider mechanism | Keep Basic git and Bearer API paths separate on one GitLab host. Placeholder literals never replace these controls. |

Upstream state was checked through GitHub and current source, not inferred from
the ledger. #3339 also merged; it concerns MCP session tools and does not itself
replace forge authentication. #3258 remains open and does not prohibit safe
literal shell configuration.

## Harness traces

| Harness | Configuration to tools | Workspace setup | GitHub/GitLab behavior and remaining gates |
| --- | --- | --- | --- |
| kagent | `adkconfig.Builder` -> serialized AgentConfig -> `agent.wireSkills` -> `NewExecutionTools` -> `CommandExecutor`; baseline filtering then exact literals | `KAgentExecutor.prepareTurnDirectory` passes the same declared literals to `workspace.Prepare` and its git subprocesses | No forge-specific setup or runtime token lookup. Declared OAuth mode and host reach the actual glab binary. |
| Claude | Claude compiler preserves Harness literals -> adapter `childEnvironment` -> driver -> native Claude -> Bash | Driver uses `workspace.Prepare`, or `PrepareAs` when changing uid; child setup reads its own supplied environment | Driver applies mirror trust and turn identity. CLI settings come only from the supplied environment. |
| Codex | Codex compiler preserves Harness literals -> adapter `nativeEnvironment` -> driver -> App Server -> shell | Driver calls `workspace.Prepare` with configured environment before the native process | Driver adds mirror trust and turn identity. Its login-shell profile restores mounted tool paths. |
| OMP | BYO compiler preserves Harness literals -> OMP adapter -> `nativeEnvironment` -> `omp --mode rpc` -> bash | Driver consumes workspace dispatch, calls workspace.Prepare with its supplied environment, and passes the Session directory as cwd | This change adds toolpath.Extend and dispatch/setup support with a real subprocess and HTTP test. Forge literals remain platform configuration; no new forge-specific helper is added. Real granted forge access still needs deployment validation. |

Claude and Codex currently pass their configured native environment, after
removing adapter-owned variables. They do not use the ADK allowlist. Do not
broaden the ADK filter to imitate that behavior or introduce real runtime secrets
into their native environment. The platform compilers must continue to pass
only inert model/MCP credentials to actors.

`PrepareAs` transports workspace options on stdin and supplies its environment
to the subprocess; `PrepareRequested` uses that process environment. Tests must
exercise that uid-changing path as well as direct setup before removal.

All image binaries precede mounted tools: `toolpath.ExtendPath` appends Harness
tool paths, then Session tool paths. ADK extends the runtime PATH before
filtering; Claude and Codex extend child environments. Codex's profile script
restores tool paths after login-shell PATH initialization. OMP now extends its
native environment too. An explicit PATH literal replaces the inherited PATH, so Harness
authors must include required directories. The browser proof selects
`/opt/kagent/tools/github/bin/gh` explicitly to measure the pinned binary.

## Replacement contract and checks

[Platform CLI configuration](https://github.com/giantswarm/agent-platform/blob/main/docs/cli-environment.md)
declares values for all four Harnesses with workspaces enabled. A configured
GitLab provider supplies its host; multiple instances require each Harness to
select its intended host. GitHub Enterprise
also needs its appropriate host and enterprise token variable, as documented in
the [GitHub CLI environment reference](https://cli.github.com/manual/gh_help_environment).
These are literal placeholders, not grant material. The helper's Basic value is
replaced by the gateway's provider binding, including the provider's actual
username and token. Deploy these declarations before consuming the runtime
release that removes CLI setup. Keep the removal as a dependent change until
the Session checks below pass.

The ADK implementation copies Harness literals into `shell_environment` for root
and shared agents. `turn.CommandEnvironment` filters first, then overlays the
exact configured values. Empty values and embedded equals signs survive.
`__KAGENT_ENV[...]__` remains literal in this map, including on shared agents;
it cannot retrieve a runtime secret. A turn's working directory and git identity
remain authoritative. No new Session API or broad TOKEN allowance is introduced.

Tests cover serialized root/shared configuration, a real bash tool subprocess,
an HTTP request emitted by curl, workspace setup's real git subprocess, runtime
secret withholding and reference-expansion bypass. Independent native protocol
fixtures observe declared GitHub and GitLab sentinels in Claude, Codex and OMP
subprocesses. Pinned glab 1.124.0, downloaded from its toolchain manifest and
verified by SHA-256, sends OAuth Bearer with GLAB_IS_OAUTH2=true and PRIVATE-TOKEN
without it, through the actual ADK shell. Its declared host selects the fixture's
`/api/v4/user`; the runtime token does not reach that request. See
[GitLab authentication](https://docs.gitlab.com/cli/authentication/) and
[configuration](https://docs.gitlab.com/cli/configuration/) for host selection.
These local tests do not establish a successful real GitLab Session grant.
An actual git HTTP Basic challenge also verifies the explicitly configured
credential helper independently of workspace helper injection.
The removal rollout requires these checks:

1. Platform-owned git, gh and glab configuration reaches actual native tools on
   each Harness and every workspace setup subprocess.
2. GitHub ordinary requests and comment read-back identify the person; GitLab
   API requests use OAuth Bearer and the intended hosted/self-hosted host.
   Attachment checks record the actual upload result independently. The GitHub
   service restriction does not block the generic environment mechanism; an
   attachment-specific replacement must preserve the measured protocol behavior
   and cannot claim successful upload without a verified screenshot comment.
3. A Session without the provider grant and one whose grant was revoked cannot
   authenticate, even with the same literal placeholders.
4. Golden-only credentials stay absent from Sessions; actor-JWT services remain
   reachable only during an authorized turn.
5. OMP's locally tested dispatch, setup and PATH replacements pass against the
   pinned native binary in a real Session.
