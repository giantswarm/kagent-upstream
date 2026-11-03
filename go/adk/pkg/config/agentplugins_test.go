package config

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kagent-dev/kagent/go/api/adk"
	"github.com/kagent-dev/kagent/go/api/agentplugin"
	"github.com/kagent-dev/kagent/go/core/pkg/agentplugins"
)

func TestMaterializeAgentPluginsIsolatesSubagentSkills(t *testing.T) {
	root := t.TempDir()
	paths := AgentPluginPaths{
		Packages: filepath.Join(root, "packages"),
		Skills:   filepath.Join(root, "skills"),
		Data:     filepath.Join(root, "data"),
	}
	repository, commit := skillRepository(t, "# Skill")
	source := agentplugin.Source{Git: &agentplugin.GitSource{URL: repository, Commit: commit}}
	agentConfig := &adk.AgentConfig{
		AgentPlugins: &agentplugin.Resources{Skills: []agentplugin.Skill{{Name: "root", Source: source}}},
		SubAgents:    []*adk.AgentConfig{{Name: "child", AgentPlugins: &agentplugin.Resources{Skills: []agentplugin.Skill{{Name: "child", Source: source}}}}},
	}
	if err := MaterializeAgentPlugins(context.Background(), agentConfig, paths); err != nil {
		t.Fatal(err)
	}
	if agentConfig.SkillsDirectory == agentConfig.SubAgents[0].SkillsDirectory {
		t.Fatalf("root and child share skills directory %q", agentConfig.SkillsDirectory)
	}
	for _, path := range []string{
		filepath.Join(agentConfig.SkillsDirectory, "root", "SKILL.md"),
		filepath.Join(agentConfig.SubAgents[0].SkillsDirectory, "child", "SKILL.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("materialized skill %q: %v", path, err)
		}
	}
}

func TestMaterializeAgentPluginsRejectsClaudeFormatPlugin(t *testing.T) {
	root := t.TempDir()
	paths := AgentPluginPaths{Packages: filepath.Join(root, "packages"), Skills: filepath.Join(root, "skills"), Data: filepath.Join(root, "data")}
	// Pre-seeded package cache: Materialize reuses it instead of fetching.
	manifest := filepath.Join(paths.Packages, "plugin-0", ".claude-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"name":"acme-claude"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	source := agentplugin.Source{Git: &agentplugin.GitSource{URL: "unused", Commit: strings.Repeat("a", 40)}}
	agentConfig := &adk.AgentConfig{AgentPlugins: &agentplugin.Resources{Plugins: []agentplugin.Bundle{{Source: source}}}}
	if err := MaterializeAgentPlugins(context.Background(), agentConfig, paths); err == nil {
		t.Fatal("MaterializeAgentPlugins() accepted a Claude-format plugin")
	}
}

func TestAddMCPConfigConvertsRuntimeNeutralServers(t *testing.T) {
	agentConfig := &adk.AgentConfig{}
	addMCPConfig(agentConfig, agentplugins.MCPConfig{
		StreamableHTTP: []agentplugins.RemoteMCPServer{{URL: "https://http.example.com", Headers: map[string]string{"X-Test": "http"}}},
		SSE:            []agentplugins.RemoteMCPServer{{URL: "https://sse.example.com", Headers: map[string]string{"X-Test": "sse"}}},
		Stdio:          []agentplugins.StdioMCPServer{{Command: "server", Args: []string{"--serve"}, Env: map[string]string{"KEY": "value"}, Dir: "/plugin"}},
	})

	if len(agentConfig.HttpTools) != 1 || agentConfig.HttpTools[0].Params.Url != "https://http.example.com" {
		t.Fatalf("HTTP tools = %#v", agentConfig.HttpTools)
	}
	if len(agentConfig.SseTools) != 1 || agentConfig.SseTools[0].Params.Url != "https://sse.example.com" {
		t.Fatalf("SSE tools = %#v", agentConfig.SseTools)
	}
	if len(agentConfig.StdioTools) != 1 || agentConfig.StdioTools[0].Command != "server" {
		t.Fatalf("stdio tools = %#v", agentConfig.StdioTools)
	}
}

// skillRepository is a local git repository holding one skill, fetchable by
// commit.
func skillRepository(t *testing.T, skill string) (repository, commit string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repository = t.TempDir()
	git := func(args ...string) string {
		command := exec.Command("git", append([]string{"-C", repository, "-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		command.Dir = t.TempDir()
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repository, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "SKILL.md")
	git("commit", "-q", "-m", "skill")
	return repository, git("rev-parse", "HEAD")
}
