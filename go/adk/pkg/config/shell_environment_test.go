package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/api/adk"
)

func TestShellEnvironmentIsNeverExpanded(t *testing.T) {
	t.Setenv("RUNTIME_SECRET", "withheld")
	t.Setenv("MISSING", "")
	require.NoError(t, os.Unsetenv("MISSING"))
	configuration := `{"model":null,"shell_environment":{"LITERAL":"__KAGENT_ENV[REFERENCE]__"},"sub_agents":[{"model":null,"shell_environment":{"LITERAL":"__KAGENT_ENV[REFERENCE]__"}}]}`
	for _, referenceName := range []string{"RUNTIME_SECRET", "MISSING"} {
		for _, fieldName := range []string{"shell_environment", "SHELL_ENVIRONMENT", "Shell_Environment"} {
			t.Run(fieldName+"/"+referenceName, func(t *testing.T) {
				input := strings.NewReplacer("shell_environment", fieldName, "REFERENCE", referenceName).Replace(configuration)
				expanded, err := expandConfigEnv(input)
				require.NoError(t, err)
				var agentConfig adk.AgentConfig
				require.NoError(t, json.Unmarshal([]byte(expanded), &agentConfig))
				literal := "__KAGENT_ENV[" + referenceName + "]__"
				require.Equal(t, literal, agentConfig.ShellEnvironment["LITERAL"])
				require.Equal(t, literal, agentConfig.SubAgents[0].ShellEnvironment["LITERAL"])
				require.NotContains(t, expanded, "withheld")
			})
		}
	}
}
