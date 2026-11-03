---
id: harness-shared-children-carry-their-own-skills
fork: kagent
theme: harness-variety
category: feature
status: carried
commits:
  - 'feat(harness): Shared children carry their own skills on the Claude and Codex harnesses'
upstream:
  outcome: open
  refs: [kagent-dev/kagent#3386, kagent-dev/kagent#3400, kagent-dev/kagent#3018]
  exit: '#3400 merges; the first re-pin onto a commit containing it drops this commit and takes #3400 whole'
depends_on: []
verified: 2026-11-03
---
# feat(harness): Shared children carry their own skills on the Claude and Codex harnesses

## Change

`feat(harness): Shared children carry their own skills on the Claude and Codex harnesses`

## Reason

A Shared child (`tools[].subAgent`) with `spec.skills` was refused by both native compilers, so a portable subagent that needs a skill ran on the kagent and omp harnesses only. The compilers now install every direct child's skills beside the root's (`CompileSharedSkillResources`: one directory per name, a name with two sources refused). Claude preloads a child's skills through the `skills` entry of its `--agents` definition, by the plain name the `--add-dir` skill root gives it; each Codex role file disables, through `skills.config`, every installed skill the child did not select. Neither is an authority boundary: the root can invoke a child's skills, and on Claude a child can invoke any installed skill. The Claude config contract moves to version 8 and the Codex one to version 5. Tests: `TestCompileSharedSkillResources`, `TestCompileLocalAgentSkills`, `TestCompileMCPAndSharedAgent`, `TestSharedAgentsDisableUnselectedSkills`.

## History

Carries part of kagent-dev/kagent#3400 (third-party, for kagent-dev/kagent#3386), open and unreviewed on 2026-11-03. Left out: Claude child MCP tools. #3400 writes a child's MCP servers inline in its `--agents` entry with their real URL and headers, which on this line would bypass the Claude harness's credential forwarder and its per-turn MCP configuration (`harness-forward-the-callers-credential-on-mcp-calls`, `harness-claude-reaches-actor-jwt-hosts-without-the`), so a child would reach an MCP server that needs the caller's token without it. The same gap between #3400 and kagent-dev/kagent#3018 upstream is raised on #3018. Also left out: #3400's e2e tests, which rely on upstream's OCI image-volume skills; the line's end-to-end proof is the agent harness matrix in giantswarm/agentlab.
