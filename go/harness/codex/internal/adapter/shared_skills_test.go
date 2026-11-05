package adapter

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/api/agentplugin"
	"github.com/kagent-dev/kagent/go/harness/codex/config"
)

func TestSharedAgentsDisableUnselectedSkills(t *testing.T) {
	durable := t.TempDir()
	names := []string{"root", "review", "deploy"}
	repo, commit := skillsRepository(t, names)
	cfg := config.Production("gpt-test", "root instructions")
	cfg.Provider = config.Provider{Name: "openai"}
	cfg.SkillResources = &agentplugin.Resources{}
	for _, name := range names {
		cfg.SkillResources.Skills = append(cfg.SkillResources.Skills, agentplugin.Skill{
			Name: name, Source: agentplugin.Source{Git: &agentplugin.GitSource{URL: "file://" + repo, Commit: commit}, Path: name},
		})
	}
	cfg.Agents = map[string]config.Agent{
		"reviewer": {Description: "Reviews", Instruction: "Review", Model: "gpt-test", Skills: []string{"review"}},
		"deployer": {Description: "Deploys", Instruction: "Deploy", Model: "gpt-test", Skills: []string{"deploy"}},
		"plain":    {Description: "No skills", Instruction: "Help", Model: "gpt-test"},
	}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	_, err = New(t.Context(), Input{ConfigJSON: raw, Workspace: filepath.Join(durable, "workspace"), DurableDir: durable})
	require.NoError(t, err)
	for role, want := range map[string][]string{"reviewer": {"deploy", "root"}, "deployer": {"review", "root"}, "plain": {"deploy", "review", "root"}} {
		raw, err := os.ReadFile(filepath.Join(durable, "codex", "agents", role+".toml"))
		require.NoError(t, err)
		var parsed nativeAgentConfig
		require.NoError(t, toml.Unmarshal(raw, &parsed))
		require.NotNil(t, parsed.Skills)
		var disabled []string
		for _, selection := range parsed.Skills.Config {
			require.False(t, selection.Enabled)
			require.Equal(t, "SKILL.md", filepath.Base(selection.Path))
			disabled = append(disabled, filepath.Base(filepath.Dir(selection.Path)))
		}
		require.Equal(t, want, disabled, role)
	}
	for _, name := range names {
		_, err := os.Stat(filepath.Join(durable, "codex", "skills", name, "SKILL.md"))
		require.NoError(t, err, "root must discover every installed skill")
	}
}

// skillsRepository commits one SKILL.md per name, each in its own directory.
func skillsRepository(t *testing.T, names []string) (string, string) {
	t.Helper()
	// The developer's git configuration (commit.gpgsign, hooks) must not reach
	// the fixture or the fetch the adapter runs from it.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
		cmd.Dir = repo
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, output)
		return strings.TrimSpace(string(output))
	}
	run("init", "-q")
	run("config", "uploadpack.allowAnySHA1InWant", "true")
	for _, name := range names {
		require.NoError(t, os.MkdirAll(filepath.Join(repo, name), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(repo, name, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Test skill.\n---\n"+name), 0o644))
	}
	run("add", ".")
	run("commit", "-q", "-m", "skills")
	return repo, run("rev-parse", "HEAD")
}
