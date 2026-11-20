package driver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/harness/runtime"
)

func TestConfiguredForgeValuesReachCodexProcess(t *testing.T) {
	t.Setenv("GH_TOKEN", "runtime-secret")
	t.Setenv("UNDECLARED_TOKEN", "runtime-secret")
	directory := t.TempDir()
	capture := filepath.Join(directory, "environment")
	executable := filepath.Join(directory, "codex")
	require.NoError(t, os.WriteFile(executable, []byte(`#!/bin/sh
printf '%s\n' "$GH_TOKEN" "$GITLAB_TOKEN" "$GLAB_IS_OAUTH2" "$GITLAB_HOST" "${UNDECLARED_TOKEN-unset}" > "$CAPTURE"
read initialize
echo '{"id":1,"result":{}}'
read initialized
read thread
echo '{"id":2,"result":{"thread":{"id":"thread-1"}}}'
read turn
echo '{"id":3,"result":{"turn":{"id":"turn-1"}}}'
echo '{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}}'
while read ignored; do :; done
`), 0o700))
	driver := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: directory, Model: "model", Provider: "provider", MaxFrameBytes: 4096, MaxStderrBytes: 4096, InterruptGrace: time.Second,
		Environment: []string{"CAPTURE=" + capture, "GH_TOKEN=github-sentinel", "GITLAB_TOKEN=gitlab-sentinel", "GLAB_IS_OAUTH2=true", "GITLAB_HOST=forge.example"},
	})
	_, err := driver.Run(t.Context(), runtime.Turn{Prompt: "work"}, &recordingSink{})
	require.NoError(t, err)
	content, err := os.ReadFile(capture)
	require.NoError(t, err)
	require.Equal(t, "github-sentinel\ngitlab-sentinel\ntrue\nforge.example\nunset\n", string(content))
}
