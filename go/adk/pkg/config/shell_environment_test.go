package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/api/adk"
)

func TestShellEnvironmentIsNeverExpanded(t *testing.T) {
	t.Setenv("RUNTIME_SECRET", "withheld")
	config := `{"model":null,"shell_environment":{"LITERAL":"__KAGENT_ENV[RUNTIME_SECRET]__"},"sub_agents":[{"model":null,"shell_environment":{"LITERAL":"__KAGENT_ENV[MISSING]__"}}]}`
	expanded, err := expandConfigEnv(config)
	require.NoError(t, err)
	var agentConfig adk.AgentConfig
	require.NoError(t, json.Unmarshal([]byte(expanded), &agentConfig))
	require.Equal(t, "__KAGENT_ENV[RUNTIME_SECRET]__", agentConfig.ShellEnvironment["LITERAL"])
	require.Equal(t, "__KAGENT_ENV[MISSING]__", agentConfig.SubAgents[0].ShellEnvironment["LITERAL"])
	require.NotContains(t, expanded, "withheld")
}
