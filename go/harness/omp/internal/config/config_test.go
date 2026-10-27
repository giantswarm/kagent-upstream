package config

import (
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/api/agentplugin"
)

var environment = map[string]string{"KAGENT_CREDENTIAL_A": "placeholder-a", "KAGENT_CREDENTIAL_B": "placeholder-b"}

func lookupEnv(name string) (string, bool) {
	value, ok := environment[name]
	return value, ok
}

func anthropic() map[string]any {
	return map[string]any{"type": "anthropic", "model": "claude-sonnet-4-5"}
}

func agent(fields map[string]any) []byte {
	document := map[string]any{"description": "d", "instruction": "Be brief.", "model": anthropic()}
	maps.Copy(document, fields)
	raw, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return raw
}

func TestParse(t *testing.T) {
	model := anthropic()
	model["base_url"] = "https://gateway.example.com/anthropic/"
	model["headers"] = map[string]string{"X-Team": "platform", "X-Key": "__KAGENT_ENV[KAGENT_CREDENTIAL_A]__"}
	model["max_tokens"] = 4096
	cfg, err := Parse(agent(map[string]any{
		"model": model,
		"http_tools": []map[string]any{
			{
				"params": map[string]any{
					"url":     "https://muster.example.com/mcp",
					"headers": map[string]string{"Authorization": "__KAGENT_ENV[KAGENT_CREDENTIAL_B]__", "X-Other": "__KAGENT_ENV[KAGENT_CREDENTIAL_A]__", "X-Plain": "v"},
					"timeout": 12.5,
				},
				"tools": []string{"list", "get"},
			},
			{"params": map[string]any{"url": "http://muster.other.example.com/mcp"}},
			{"params": map[string]any{"url": "http://10.0.0.1:8080/mcp"}},
		},
	}), lookupEnv)
	require.NoError(t, err)

	assert.Equal(t, Model{
		ID: "claude-sonnet-4-5", BaseURL: "https://gateway.example.com/anthropic",
		Headers: map[string]string{"X-Team": "platform", "X-Key": "placeholder-a"}, MaxTokens: 4096,
	}, cfg.Model)
	assert.Equal(t, "Be brief.", cfg.Instruction)
	require.Len(t, cfg.MCPServers, 3)
	assert.Equal(t, MCPServer{
		Name: "muster", URL: "https://muster.example.com/mcp",
		Headers:         map[string]string{"Authorization": "placeholder-b", "X-Other": "placeholder-a", "X-Plain": "v"},
		HeaderVariables: []string{"KAGENT_CREDENTIAL_A", "KAGENT_CREDENTIAL_B"},
		Tools:           []string{"list", "get"}, TimeoutSeconds: 12.5,
	}, cfg.MCPServers[0])
	assert.Equal(t, "muster_2", cfg.MCPServers[1].Name, "a second server on the same host label gets a suffix")
	assert.Equal(t, "10", cfg.MCPServers[2].Name)
}

func TestParseCarriesThePlugins(t *testing.T) {
	commit := strings.Repeat("a", 40)
	cfg, err := Parse(agent(map[string]any{"agent_plugins": map[string]any{"plugins": []map[string]any{{
		"source": map[string]any{"git": map[string]any{"url": "https://example.com/plugins.git", "commit": commit}},
		"skills": []string{"review"},
	}}}}), lookupEnv)
	require.NoError(t, err)
	require.NotNil(t, cfg.Skills)
	assert.Equal(t, []agentplugin.Bundle{{
		Source: agentplugin.Source{Git: &agentplugin.GitSource{URL: "https://example.com/plugins.git", Commit: commit}},
		Skills: []string{"review"},
	}}, cfg.Skills.Plugins)
}

func TestParseDefaultsTheAnthropicEndpoint(t *testing.T) {
	cfg, err := Parse(agent(nil), lookupEnv)
	require.NoError(t, err)
	assert.Equal(t, DefaultAnthropicBaseURL, cfg.Model.BaseURL)
	assert.Empty(t, cfg.MCPServers)
}

func TestParseRefuses(t *testing.T) {
	withModel := func(name string, value any) map[string]any {
		model := anthropic()
		model[name] = value
		return map[string]any{"model": model}
	}
	server := func(params map[string]any, extra map[string]any) map[string]any {
		entry := map[string]any{"params": params}
		maps.Copy(entry, extra)
		return map[string]any{"http_tools": []map[string]any{entry}}
	}
	url := "https://mcp.example.com/mcp"
	for name, test := range map[string]struct {
		fields map[string]any
		want   string
	}{
		"openai model":     {map[string]any{"model": map[string]any{"type": "openai", "model": "gpt"}}, `needs an Anthropic ModelConfig, got "openai"`},
		"no model name":    {withModel("model", " "), "names no model"},
		"key passthrough":  {withModel("api_key_passthrough", true), "API key passthrough"},
		"model TLS":        {withModel("tls_insecure_skip_verify", true), "custom model TLS"},
		"model URL":        {withModel("base_url", "gateway:443"), "model base URL must be an absolute HTTP(S) URL"},
		"unset variable":   {withModel("headers", map[string]string{"X": "__KAGENT_ENV[MISSING]__"}), "environment variable MISSING is not set"},
		"SSE server":       {map[string]any{"sse_tools": []map[string]any{{"params": map[string]any{"url": url}}}}, "SSE MCP servers"},
		"stdio server":     {map[string]any{"stdio_tools": []map[string]any{{"params": map[string]any{"command": "x"}}}}, "stdio MCP servers"},
		"remote agent":     {map[string]any{"remote_agents": []map[string]any{{"name": "r", "url": "https://agent.example.com"}}}, "remote agent bindings"},
		"memory":           {map[string]any{"memory": map[string]any{}}, "memory is not supported"},
		"approval":         {server(map[string]any{"url": url}, map[string]any{"require_approval": true}), "tool approval"},
		"server TLS":       {server(map[string]any{"url": url, "tls_insecure_skip_verify": true}, nil), "custom TLS"},
		"server URL":       {server(map[string]any{"url": "file:///etc/passwd"}, nil), "MCP server 0 URL must be an absolute HTTP(S) URL"},
		"malformed config": {map[string]any{"http_tools": "nope"}, "decode agent configuration"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(agent(test.fields), lookupEnv)
			assert.ErrorContains(t, err, test.want)
		})
	}
}

func shared(name string, fields map[string]any) map[string]any {
	child := map[string]any{"name": name, "description": "Reviews diffs.", "instruction": "Review.", "model": anthropic()}
	maps.Copy(child, fields)
	return child
}

func pluginBundle(url string, skills ...string) map[string]any {
	return map[string]any{
		"source": map[string]any{"git": map[string]any{"url": url, "commit": strings.Repeat("a", 40)}},
		"skills": skills,
	}
}

func TestParseSharedAgents(t *testing.T) {
	haiku := anthropic()
	haiku["model"] = "claude-haiku-4-5"
	cfg, err := Parse(agent(map[string]any{
		"agent_plugins": map[string]any{"plugins": []map[string]any{pluginBundle("https://example.com/a.git", "lint")}},
		"sub_agents": []map[string]any{
			shared("reviewer-x", map[string]any{
				"model": haiku,
				"agent_plugins": map[string]any{
					"skills":  []map[string]any{{"name": "style", "source": map[string]any{"oci": "registry.example.com/style:1"}}},
					"plugins": []map[string]any{pluginBundle("https://example.com/a.git", "lint", "review"), pluginBundle("https://example.com/b.git", "docs")},
				},
			}),
			shared("same-model", nil),
		},
	}), lookupEnv)
	require.NoError(t, err)

	assert.Equal(t, []SharedAgent{
		{Name: "reviewer-x", Description: "Reviews diffs.", Instruction: "Review.", ModelID: "claude-haiku-4-5", Skills: []string{"style", "lint", "review", "docs"}},
		{Name: "same-model", Description: "Reviews diffs.", Instruction: "Review.", ModelID: "claude-sonnet-4-5"},
	}, cfg.SharedAgents)
	assert.Equal(t, []string{"claude-sonnet-4-5", "claude-haiku-4-5"}, cfg.ModelIDs())
	require.NotNil(t, cfg.Skills)
	assert.Equal(t, []agentplugin.Skill{{Name: "style", Source: agentplugin.Source{OCI: "registry.example.com/style:1"}}}, cfg.Skills.Skills)
	commit := strings.Repeat("a", 40)
	assert.Equal(t, []agentplugin.Bundle{
		{Source: agentplugin.Source{Git: &agentplugin.GitSource{URL: "https://example.com/a.git", Commit: commit}}, Skills: []string{"lint", "review"}},
		{Source: agentplugin.Source{Git: &agentplugin.GitSource{URL: "https://example.com/b.git", Commit: commit}}, Skills: []string{"docs"}},
	}, cfg.Skills.Plugins, "a skill the root and a Shared agent both select from one source is installed once")
}

func TestParseRefusesSharedAgents(t *testing.T) {
	withModel := func(name string, value any) map[string]any {
		model := anthropic()
		model[name] = value
		return map[string]any{"model": model}
	}
	for name, test := range map[string]struct {
		child map[string]any
		want  string
	}{
		"path name":       {shared("../escape", nil), "binding name of letters"},
		"reserved name":   {shared("Task", nil), "omp reserves"},
		"no description":  {shared("x", map[string]any{"description": " "}), "has no description"},
		"MCP tools":       {shared("x", map[string]any{"http_tools": []map[string]any{{"params": map[string]any{"url": "https://mcp.example.com/mcp"}}}}), "see every MCP tool of the root"},
		"nested":          {shared("x", map[string]any{"sub_agents": []map[string]any{shared("y", nil)}}), "nested agent bindings"},
		"remote":          {shared("x", map[string]any{"remote_agents": []map[string]any{{"name": "r", "url": "https://agent.example.com"}}}), "nested agent bindings"},
		"memory":          {shared("x", map[string]any{"memory": map[string]any{}}), "memory is not supported"},
		"other provider":  {shared("x", map[string]any{"model": map[string]any{"type": "openai", "model": "gpt"}}), `shared agent "x": the omp Harness needs an Anthropic ModelConfig`},
		"other endpoint":  {shared("x", withModel("base_url", "https://other.example.com")), "root agent's provider and authentication"},
		"other headers":   {shared("x", withModel("headers", map[string]string{"X-Team": "other"})), "root agent's provider and authentication"},
		"other maxTokens": {shared("x", withModel("max_tokens", 10)), "root agent's provider and authentication"},
		"skill clash": {shared("x", map[string]any{"agent_plugins": map[string]any{
			"plugins": []map[string]any{pluginBundle("https://example.com/other.git", "lint")},
		}}), `shared agent "x": skill "lint" is selected from two sources`},
		"duplicate skill": {shared("x", map[string]any{"agent_plugins": map[string]any{
			"plugins": []map[string]any{pluginBundle("https://example.com/b.git", "docs"), pluginBundle("https://example.com/c.git", "docs")},
		}}), `duplicate skill name "docs"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(agent(map[string]any{
				"agent_plugins": map[string]any{"plugins": []map[string]any{pluginBundle("https://example.com/a.git", "lint")}},
				"sub_agents":    []map[string]any{test.child},
			}), lookupEnv)
			assert.ErrorContains(t, err, test.want)
		})
	}
}
