package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/harness/codex/config"
	"github.com/kagent-dev/kagent/go/harness/runtime"
	"github.com/kagent-dev/mockllm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// codexBinaryEnv names the pinned Codex CLI the integration test runs; the
// test is skipped without it, as the binary is not part of the module.
const codexBinaryEnv = "KAGENT_CODEX_BINARY"

// signInModel scripts a turn that finds the MCP tool mcp__stub.whoami, calls
// it and answers once it returns.
const signInModel = `{"openai_response":[
 {"name":"search","match":{"match_type":"contains","input":"who am i"},
  "response":{"id":"r1","object":"response","created_at":1,"model":"gpt-5.2-codex","parallel_tool_calls":false,"tools":[],
   "output":[{"id":"ts1","type":"tool_search_call","status":"completed","call_id":"search_1","execution":"client","arguments":{"query":"mcp__stub whoami","limit":10}}],
   "usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}},
 {"name":"call","match":{"match_type":"contains","input":"search_1"},
  "response":{"id":"r2","object":"response","created_at":2,"model":"gpt-5.2-codex","parallel_tool_calls":false,"tools":[],
   "output":[{"id":"fc1","type":"function_call","status":"completed","call_id":"call_1","namespace":"mcp__stub","name":"whoami","arguments":"{}"}],
   "usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}},
 {"name":"answer","match":{"match_type":"contains","input":"Output:"},
  "response":{"id":"r3","object":"response","created_at":3,"model":"gpt-5.2-codex","parallel_tool_calls":false,"tools":[],
   "output":[{"id":"m1","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"SIGNED_IN_DONE","annotations":[]}]}],
   "usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}
]}`

// TestCodexRelaysAURLElicitation runs the pinned Codex against a fake
// Responses endpoint and an MCP server whose tool answers with a URL
// elicitation: an input_required result on MCP 2026-07-28, -32042 on earlier
// revisions, which Codex fails the call on. The turn parks on it, and once
// accepted Codex retries the call, which then succeeds.
func TestCodexRelaysAURLElicitation(t *testing.T) {
	binary := os.Getenv(codexBinaryEnv)
	if binary == "" {
		t.Skipf("%s is not set", codexBinaryEnv)
	}
	var calls, accepted atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "stub", Version: "0.0.1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "whoami", Description: "Names the person"}, func(_ context.Context, request *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		calls.Add(1)
		if response, ok := request.Params.InputResponses["sign_in"].(*mcp.ElicitResult); ok && response.Action == "accept" {
			accepted.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "jane"}}}, nil, nil
		}
		signIn := &mcp.ElicitParams{Mode: "url", Message: "Sign in to stub", URL: "https://sign-in.example/start?s=1", ElicitationID: "e1"}
		// An earlier revision gets -32042, as a gateway that holds no call open
		// for the sign-in answers it, not the SDK's server-initiated fallback.
		if request.ProtocolVersion() < "2026-07-28" {
			return nil, nil, mcp.URLElicitationRequiredError([]*mcp.ElicitParams{signIn})
		}
		return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"sign_in": signIn}}, nil, nil
	})
	mcpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(mcpServer.Close)

	var model mockllm.Config
	require.NoError(t, json.Unmarshal([]byte(signInModel), &model))
	model.ListenAddr = "127.0.0.1:0"
	modelServer := mockllm.NewServer(model)
	modelURL, err := modelServer.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = modelServer.Stop(context.Background()) })

	cfg := config.Production("gpt-5.2-codex", "Use the configured MCP tool.")
	cfg.CodexExecutable = binary
	cfg.Provider = config.Provider{Name: config.ProviderOpenAI, BaseURL: strings.TrimSuffix(modelURL, "/") + "/v1"}
	cfg.MCPServers = map[string]config.MCPServer{"stub": {URL: mcpServer.URL}}
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	durable := filepath.Join(t.TempDir(), "data")
	d, err := New(t.Context(), Input{
		ConfigJSON: raw, Workspace: filepath.Join(durable, "workspace"), DurableDir: durable,
		Environment: append(os.Environ(), "OPENAI_API_KEY=fake", "HOME="+durable),
	})
	require.NoError(t, err)

	sink := &textSink{}
	outcome, err := d.Run(t.Context(), runtime.Turn{Prompt: "who am i"}, sink)
	require.NoError(t, err)
	require.Nil(t, outcome.Failure, "failure = %#v", outcome.Failure)
	require.NotNil(t, outcome.Pending, "the URL elicitation parks the turn")
	request, ok := outcome.Pending.Request().(*runtime.URLElicitationRequest)
	require.True(t, ok, "request = %#v", outcome.Pending.Request())
	require.Equal(t, "stub", request.Server)
	require.Equal(t, "https://sign-in.example/start?s=1", request.URL)
	require.Zero(t, accepted.Load())

	done, err := outcome.Pending.Resume(t.Context(), &runtime.URLElicitationResponse{ID: request.ID, Action: apia2a.URLElicitationAccept}, sink)
	require.NoError(t, err)
	require.Nil(t, done.Pending)
	require.Nil(t, done.Failure, "failure = %#v", done.Failure)
	require.Equal(t, int32(1), accepted.Load(), "Codex retries the call with the accepted sign-in")
	require.Contains(t, sink.text.String(), "SIGNED_IN_DONE")
}

type textSink struct{ text strings.Builder }

func (*textSink) SessionStarted(runtime.SessionStarted) error { return nil }
func (*textSink) Notice(runtime.Notice) error                 { return nil }
func (s *textSink) TextDelta(event runtime.TextDelta) error {
	s.text.WriteString(event.Text)
	return nil
}
func (*textSink) ToolCall(runtime.ToolCall) error     { return nil }
func (*textSink) ToolResult(runtime.ToolResult) error { return nil }
