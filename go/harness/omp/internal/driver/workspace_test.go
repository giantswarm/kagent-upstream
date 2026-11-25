package driver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/api/adk"
	"github.com/kagent-dev/kagent/go/harness/runtime"
)

func TestWorkspaceDispatchReachesOMPProcess(t *testing.T) {
	for _, binary := range []string{"git", "curl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skip(binary + " is required")
		}
	}
	t.Setenv("UNDECLARED_TOKEN", "runtime-secret")
	directory := t.TempDir()
	origin := filepath.Join(directory, "origin")
	mirrors := filepath.Join(directory, "mirrors")
	sessionDirectory := filepath.Join(directory, "session")
	require.NoError(t, os.MkdirAll(sessionDirectory, 0o755))
	for _, arguments := range [][]string{
		{"init", "--quiet", "--initial-branch=main", origin},
		{"-C", origin, "-c", "user.name=test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "--message", "seed"},
		{"clone", "--quiet", "--mirror", "file://" + origin, filepath.Join(mirrors, "o/r.git")},
	} {
		output, err := exec.CommandContext(t.Context(), "git", arguments...).CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		headers <- request.Header.Clone()
		fmt.Fprint(writer, "accepted")
	}))
	defer server.Close()
	executable := filepath.Join(directory, "omp")
	require.NoError(t, os.WriteFile(executable, []byte(`#!/usr/bin/env bash
set -euo pipefail
pwd > "$CAPTURE.cwd"
printf '%s\n' "$@" > "$CAPTURE.args"
printf '%s\n' "$GH_TOKEN" "$GITLAB_TOKEN" "$GLAB_IS_OAUTH2" "$GITLAB_HOST" "$GLAB_GIT_PROTOCOL" "${UNDECLARED_TOKEN-unset}" > "$CAPTURE.env"
curl --fail --silent --noproxy '*' -H "Authorization: Bearer $GITLAB_TOKEN" "$PROTOCOL_URL" > "$CAPTURE.http"
read -r command
echo '{"type":"response","id":"kagent-1","command":"get_state","success":true,"data":{"sessionId":"session-1"}}'
read -r command
echo '{"type":"response","id":"kagent-2","command":"get_session_stats","success":true,"data":{}}'
read -r command
echo '{"type":"prompt_result","id":"kagent-3","status":"completed","sessionSettled":true}'
read -r command
echo '{"type":"response","id":"kagent-4","command":"get_session_stats","success":true,"data":{}}'
cat >/dev/null
`), 0o700))
	capture := filepath.Join(directory, "capture")
	driver := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: directory, SessionDir: directory,
		Args: []string{"--cwd", directory}, MaxFrameBytes: 4096, MaxStderrBytes: 4096, InterruptGrace: time.Second,
		Environment: []string{"PATH=" + os.Getenv("PATH"), "CAPTURE=" + capture, "PROTOCOL_URL=" + server.URL,
			"GH_TOKEN=github-sentinel", "GITLAB_TOKEN=gitlab-sentinel", "GLAB_IS_OAUTH2=true", "GITLAB_HOST=forge.example", "GLAB_GIT_PROTOCOL=https"},
	})
	ctx, _ := a2asrv.NewCallContext(t.Context(), a2asrv.NewServiceParams(map[string][]string{
		adk.WorkingDirectoryHeader: {sessionDirectory}, adk.MirrorsDirectoryHeader: {mirrors},
	}))
	_, err := driver.Run(ctx, runtime.Turn{Prompt: "work"}, &recordingSink{})
	require.NoError(t, err)
	for suffix, expected := range map[string]string{
		".cwd": sessionDirectory + "\n", ".env": "github-sentinel\ngitlab-sentinel\ntrue\nforge.example\nhttps\nunset\n", ".http": "accepted",
	} {
		content, err := os.ReadFile(capture + suffix)
		require.NoError(t, err)
		require.Equal(t, expected, string(content))
	}
	arguments, err := os.ReadFile(capture + ".args")
	require.NoError(t, err)
	require.Contains(t, string(arguments), "--cwd\n"+sessionDirectory+"\n")
	_, err = os.Stat(filepath.Join(sessionDirectory, "o/r/.git/objects/info/alternates"))
	require.NoError(t, err)
	select {
	case header := <-headers:
		require.Equal(t, "Bearer gitlab-sentinel", header.Get("Authorization"))
	default:
		t.Fatal("native subprocess sent no request")
	}
}
