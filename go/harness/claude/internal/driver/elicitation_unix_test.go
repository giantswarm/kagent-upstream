//go:build unix

package driver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/harness/runtime"
	"github.com/stretchr/testify/require"
)

// elicitingClaude is a Claude that asks its host for a form elicitation, a
// control request no host serves and a URL elicitation, writing each answer
// it reads to CAPTURE, then reports its result and exits once its input ends.
// It ignores the interrupt, so a cancel's answer is read before the driver
// kills it.
func elicitingClaude(t *testing.T) (dir, executable, capture string) {
	t.Helper()
	dir = t.TempDir()
	executable = filepath.Join(dir, "claude")
	capture = filepath.Join(dir, "capture")
	script := `#!/bin/sh
trap '' INT
read -r prompt
printf '%s\n' '{"type":"system","subtype":"init","session_id":"11111111-1111-4111-8111-111111111111"}'
printf '%s\n' '{"type":"control_request","request_id":"form-1","request":{"subtype":"elicitation","mcp_server_name":"github","message":"Your token?","mode":"form","requested_schema":{"type":"object"}}}'
read -r answer; printf '%s\n' "$answer" >> "$CAPTURE"
printf '%s\n' '{"type":"control_request","request_id":"dialog-1","request":{"subtype":"request_user_dialog","dialog_kind":"x"}}'
read -r answer; printf '%s\n' "$answer" >> "$CAPTURE"
printf '%s\n' '{"type":"control_request","request_id":"url-1","request":{"subtype":"elicitation","mcp_server_name":"github","message":"Sign in to github","mode":"url","url":"https://gateway.example/oauth/proxy/start?s=1","elicitation_id":"e1"}}'
read -r answer; printf '%s\n' "$answer" >> "$CAPTURE"
printf '%s\n' '{"type":"result","subtype":"success","result":"done","session_id":"11111111-1111-4111-8111-111111111111","num_turns":1}'
cat >/dev/null
printf 'input ended\n' >> "$CAPTURE"
`
	require.NoError(t, os.WriteFile(executable, []byte(script), 0o700))
	return dir, executable, capture
}

type controlAnswer struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string         `json:"subtype"`
		RequestID string         `json:"request_id"`
		Response  map[string]any `json:"response"`
		Error     string         `json:"error"`
	} `json:"response"`
}

func readAnswers(t *testing.T, capture string) ([]controlAnswer, []string) {
	t.Helper()
	raw, err := os.ReadFile(capture)
	require.NoError(t, err)
	var answers []controlAnswer
	var other []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var answer controlAnswer
		if json.Unmarshal([]byte(line), &answer) == nil && answer.Type == "control_response" {
			answers = append(answers, answer)
			continue
		}
		other = append(other, line)
	}
	return answers, other
}

func TestProcessDriverParksAURLElicitationAndAnswersIt(t *testing.T) {
	dir, executable, capture := elicitingClaude(t)
	d := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: dir, Environment: []string{"CAPTURE=" + capture},
		MaxEventBytes: 4096, MaxStderrBytes: 1024, InterruptGrace: time.Second,
	})
	sink := &recordingSink{}
	outcome, err := d.Run(t.Context(), runtime.Turn{Prompt: "list my repositories"}, sink)
	require.NoError(t, err)
	require.NotNil(t, outcome.Pending, "a URL elicitation parks the turn")
	require.Equal(t, &runtime.URLElicitationRequest{
		ID: "url-1", Server: "github", Message: "Sign in to github",
		URL: "https://gateway.example/oauth/proxy/start?s=1", ElicitationID: "e1",
	}, outcome.Pending.Request())

	_, err = outcome.Pending.Resume(t.Context(), &runtime.ApprovalDecision{ID: "url-1", Approved: true}, sink)
	require.Error(t, err, "an approval does not answer a URL elicitation")

	outcome, err = d.Run(t.Context(), runtime.Turn{Prompt: "list my repositories"}, sink)
	require.NoError(t, err)
	require.NotNil(t, outcome.Pending)
	done, err := outcome.Pending.Resume(t.Context(), &runtime.URLElicitationResponse{ID: "url-1", Action: apia2a.URLElicitationAccept}, sink)
	require.NoError(t, err)
	require.Nil(t, done.Pending)
	require.Nil(t, done.Failure)

	answers, other := readAnswers(t, capture)
	// The first run's answers come before the second run's.
	require.Len(t, answers, 5)
	second := answers[2:]
	require.Equal(t, "form-1", second[0].Response.RequestID)
	require.Equal(t, map[string]any{"action": "decline"}, second[0].Response.Response, "a form is never relayed")
	require.Equal(t, "dialog-1", second[1].Response.RequestID)
	require.Equal(t, "error", second[1].Response.Subtype)
	require.Equal(t, "url-1", second[2].Response.RequestID)
	require.Equal(t, "success", second[2].Response.Subtype)
	require.Equal(t, map[string]any{"action": "accept"}, second[2].Response.Response)
	require.Contains(t, other, "input ended", "the input closes at the result so Claude exits")
}

func TestProcessDriverCancelAnswersAURLElicitationAsCanceled(t *testing.T) {
	dir, executable, capture := elicitingClaude(t)
	d := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: dir, Environment: []string{"CAPTURE=" + capture},
		MaxEventBytes: 4096, MaxStderrBytes: 1024, InterruptGrace: time.Second,
	})
	outcome, err := d.Run(t.Context(), runtime.Turn{Prompt: "list my repositories"}, &recordingSink{})
	require.NoError(t, err)
	require.NotNil(t, outcome.Pending)
	require.NoError(t, outcome.Pending.Cancel(t.Context()))

	answers, _ := readAnswers(t, capture)
	require.Len(t, answers, 3)
	require.Equal(t, "url-1", answers[2].Response.RequestID)
	require.Equal(t, map[string]any{"action": "cancel"}, answers[2].Response.Response)
}
