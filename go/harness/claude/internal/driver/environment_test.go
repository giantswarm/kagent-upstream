package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/harness/runtime"
)

func TestConfiguredForgeValuesReachClaudeProcess(t *testing.T) {
	t.Setenv("GH_TOKEN", "runtime-secret")
	t.Setenv("UNDECLARED_TOKEN", "runtime-secret")
	directory := t.TempDir()
	capture := filepath.Join(directory, "environment")
	executable := filepath.Join(directory, "claude")
	require.NoError(t, os.WriteFile(executable, []byte(`#!/bin/sh
printf '%s\n' "$GH_TOKEN" "$GITLAB_TOKEN" "$GLAB_IS_OAUTH2" "$GITLAB_HOST" "$GLAB_GIT_PROTOCOL" "${UNDECLARED_TOKEN-unset}" > "$CAPTURE"
printf '%s\n' '{"type":"system","subtype":"init","session_id":"11111111-1111-4111-8111-111111111111"}' '{"type":"result","subtype":"success","session_id":"11111111-1111-4111-8111-111111111111"}'
cat >/dev/null
`), 0o700))
	driver := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: directory, MaxEventBytes: 4096, MaxStderrBytes: 4096, InterruptGrace: time.Second,
		Environment: []string{"CAPTURE=" + capture, "GH_TOKEN=github-sentinel", "GITLAB_TOKEN=gitlab-sentinel", "GLAB_IS_OAUTH2=true", "GITLAB_HOST=forge.example", "GLAB_GIT_PROTOCOL=https"},
	})
	_, err := driver.Run(t.Context(), runtime.Turn{Prompt: "work"}, &recordingSink{})
	require.NoError(t, err)
	content, err := os.ReadFile(capture)
	require.NoError(t, err)
	require.Equal(t, "github-sentinel\ngitlab-sentinel\ntrue\nforge.example\nhttps\nunset\n", string(content))
}
