package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/harness/runtime"
	"github.com/kagent-dev/mockllm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// claudeBinaryEnv names the pinned Claude Code binary the integration test
// runs; the test is skipped without it, as the binary is not part of the
// module.
const claudeBinaryEnv = "KAGENT_CLAUDE_BINARY"

// signInModel scripts a turn that calls the MCP tool whoami and answers once
// the tool returns.
const signInModel = `{"anthropic":[
 {"name":"call","match":{"match_type":"contains","message":{"role":"user","content":[{"type":"text","text":"who am i"}]}},
  "response":{"id":"m1","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"mcp__stub__whoami","input":{}}],"model":"claude-sonnet-4-5","stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}},
 {"name":"answer","match":{"match_type":"contains","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1"}]}},
  "response":{"id":"m2","type":"message","role":"assistant","content":[{"type":"text","text":"SIGNED_IN_DONE"}],"model":"claude-sonnet-4-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}},
 {"name":"auxiliary","match":{"match_type":"contains","message":{"role":"user","content":[{"type":"text","text":""}]}},
  "response":{"id":"m3","type":"message","role":"assistant","content":[{"type":"text","text":"auxiliary"}],"model":"claude-sonnet-4-5","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}}
]}`

// TestClaudeRelaysAURLElicitation runs the pinned Claude Code against a fake
// Anthropic endpoint and an MCP server whose tool first asks for a sign-in:
// an input_required result on MCP 2026-07-28, which a stateless server
// serves, and -32042 on 2025-11-25, which a stateful one negotiates. The turn
// parks on it, and once accepted Claude retries the call, which then succeeds.
func TestClaudeRelaysAURLElicitation(t *testing.T) {
	binary := os.Getenv(claudeBinaryEnv)
	if binary == "" {
		t.Skipf("%s is not set", claudeBinaryEnv)
	}
	for _, test := range []struct {
		name      string
		stateless bool
		protocol  string
	}{
		{name: "input required", stateless: true, protocol: "2026-07-28"},
		{name: "-32042", stateless: false, protocol: "2025-11-25"},
	} {
		t.Run(test.name, func(t *testing.T) {
			relayURLElicitation(t, binary, test.stateless, test.protocol)
		})
	}
}

func relayURLElicitation(t *testing.T, binary string, stateless bool, protocol string) {
	var calls, signedIn atomic.Int32
	signIn := &mcp.ElicitParams{Mode: "url", Message: "Sign in to stub", URL: "https://sign-in.example/start?s=1", ElicitationID: "e1"}
	server := mcp.NewServer(&mcp.Implementation{Name: "stub", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "whoami", Description: "Names the person"}, func(_ context.Context, request *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		calls.Add(1)
		require.Equal(t, protocol, request.ProtocolVersion())
		// On 2025-11-25 the retry after the sign-in carries nothing; on
		// 2026-07-28 it carries the person's answer.
		accepted := protocol < "2026-07-28" && calls.Load() > 1
		if response, ok := request.Params.InputResponses["sign_in"].(*mcp.ElicitResult); ok {
			accepted = response.Action == "accept"
		}
		if accepted {
			signedIn.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "jane"}}}, nil, nil
		}
		if protocol < "2026-07-28" {
			return nil, nil, mcp.URLElicitationRequiredError([]*mcp.ElicitParams{signIn})
		}
		return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"sign_in": signIn}}, nil, nil
	})
	mcpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: stateless}))
	t.Cleanup(mcpServer.Close)

	var config mockllm.Config
	require.NoError(t, json.Unmarshal([]byte(signInModel), &config))
	config.ListenAddr = "127.0.0.1:0"
	model := mockllm.NewServer(config)
	modelURL, err := model.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = model.Stop(context.Background()) })

	dir := t.TempDir()
	mcpConfig := filepath.Join(dir, "mcp.json")
	require.NoError(t, os.WriteFile(mcpConfig, fmt.Appendf(nil, `{"mcpServers":{"stub":{"type":"http","url":%q}}}`, mcpServer.URL), 0o600))
	d := NewProcessDriver(ProcessConfig{
		Executable: binary, Workspace: dir, MCPConfigPath: mcpConfig, Model: "claude-sonnet-4-5",
		Environment: []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "ANTHROPIC_BASE_URL=" + modelURL, "ANTHROPIC_API_KEY=fake",
			"DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		},
		MaxEventBytes: 1 << 20, MaxStderrBytes: 1 << 16, InterruptGrace: time.Second,
	})
	sink := &recordingSink{}
	outcome, err := d.Run(t.Context(), runtime.Turn{Prompt: "who am i"}, sink)
	require.NoError(t, err)
	require.Nil(t, outcome.Failure, "failure = %#v", outcome.Failure)
	require.NotNil(t, outcome.Pending, "the URL elicitation parks the turn")
	request, ok := outcome.Pending.Request().(*runtime.URLElicitationRequest)
	require.True(t, ok, "request = %#v", outcome.Pending.Request())
	require.Equal(t, "stub", request.Server)
	require.Equal(t, "https://sign-in.example/start?s=1", request.URL)
	require.Zero(t, signedIn.Load())

	done, err := outcome.Pending.Resume(t.Context(), &runtime.URLElicitationResponse{ID: request.ID, Action: apia2a.URLElicitationAccept}, sink)
	require.NoError(t, err)
	require.Nil(t, done.Pending)
	require.Nil(t, done.Failure, "failure = %#v", done.Failure)
	require.Equal(t, int32(1), signedIn.Load(), "Claude retries the call once the sign-in is accepted")
	require.True(t, strings.Contains(sink.text.String(), "SIGNED_IN_DONE"), "text = %q", sink.text.String())
}
