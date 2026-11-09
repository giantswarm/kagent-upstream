package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"
	"google.golang.org/adk/v2/tool/toolconfirmation"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
)

// hitlCaller is the context of an A2A call whose client activated the HITL
// extension, or did not.
func hitlCaller(t *testing.T, activated bool) context.Context {
	t.Helper()
	ctx, callCtx := a2asrv.NewCallContext(t.Context(), nil)
	if activated {
		extension := apia2a.HITLExtension()
		callCtx.Extensions().Activate(&extension)
	}
	return ctx
}

// toolContext is the part of a tool context the sign-in path uses.
type toolContext struct {
	adkagent.Context
	ctx          context.Context
	confirmation *toolconfirmation.ToolConfirmation
	actions      session.EventActions
	hint         string
	payload      any
}

func (c *toolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return c.confirmation }
func (c *toolContext) Actions() *session.EventActions                       { return &c.actions }
func (c *toolContext) FunctionCallID() string                               { return "call-1" }
func (c *toolContext) RequestConfirmation(hint string, payload any) error {
	c.hint, c.payload = hint, payload
	return nil
}

func (c *toolContext) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c *toolContext) Done() <-chan struct{}       { return c.ctx.Done() }
func (c *toolContext) Err() error                  { return c.ctx.Err() }
func (c *toolContext) Value(key any) any           { return c.ctx.Value(key) }

// signInServer serves the tool whoami, which asks for a sign-in until the
// person signed in: with an input_required result on MCP 2026-07-28, which a
// stateless server serves, and with -32042 on 2025-11-25.
func signInServer(t *testing.T, stateless bool, signedIn *atomic.Bool, calls *atomic.Int32) string {
	t.Helper()
	signIn := &mcpsdk.ElicitParams{Mode: "url", Message: "Sign in to github", URL: "https://gateway.example/oauth/proxy/start?s=1", ElicitationID: "e1"}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "GitHub", Version: "0.0.1"}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: "whoami", Description: "Names the person"}, func(_ context.Context, request *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, any, error) {
		calls.Add(1)
		if signedIn.Load() {
			return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "jane"}}}, nil, nil
		}
		if request.ProtocolVersion() < "2026-07-28" {
			return nil, nil, mcpsdk.URLElicitationRequiredError([]*mcpsdk.ElicitParams{signIn})
		}
		if response, ok := request.Params.InputResponses["sign_in"].(*mcpsdk.ElicitResult); ok {
			return &mcpsdk.CallToolResult{IsError: true, Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "sign-in " + response.Action}}}, nil, nil
		}
		return &mcpsdk.CallToolResult{InputRequests: mcpsdk.InputRequestMap{"sign_in": signIn}}, nil, nil
	})
	httpServer := httptest.NewServer(mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, &mcpsdk.StreamableHTTPOptions{Stateless: stateless}))
	t.Cleanup(func() {
		// A stateful session keeps the client's stream open.
		httpServer.CloseClientConnections()
		httpServer.Close()
	})
	return httpServer.URL
}

func TestSignInToolAsksThePersonAndRunsOnceConfirmed(t *testing.T) {
	for _, test := range []struct {
		name      string
		stateless bool
	}{
		{name: "input required", stateless: true},
		{name: "-32042", stateless: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var signedIn atomic.Bool
			var calls atomic.Int32
			serverURL := signInServer(t, test.stateless, &signedIn, &calls)
			toolset, err := mcptoolset.New(mcptoolset.Config{
				Transport: &mcpsdk.StreamableClientTransport{Endpoint: serverURL},
				Client:    newElicitingClient("gateway"),
			})
			require.NoError(t, err)
			wrapped := &signInToolset{inner: toolset, server: "gateway"}
			ctx := &toolContext{ctx: hitlCaller(t, true)}
			tools, err := wrapped.Tools(ctx)
			require.NoError(t, err)
			require.Len(t, tools, 1)
			whoami, ok := tools[0].(*signInTool)
			require.True(t, ok, "tool = %T", tools[0])

			_, err = whoami.Run(ctx, map[string]any{})
			require.ErrorIs(t, err, tool.ErrConfirmationRequired)
			payload, ok := ctx.payload.(map[string]any)
			require.True(t, ok, "payload = %#v", ctx.payload)
			signIn, ok := payload[SignInPayloadKey].(*SignIn)
			require.True(t, ok, "payload = %#v", payload)
			require.Equal(t, "https://gateway.example/oauth/proxy/start?s=1", signIn.URL)
			require.Equal(t, "Sign in to github", signIn.Message)
			require.Equal(t, "e1", signIn.ElicitationID)
			require.Equal(t, "gateway", signIn.Server, "the configured name, not the one the server reports")
			require.Equal(t, "Sign in to github", ctx.hint)
			require.True(t, ctx.actions.SkipSummarization)

			signedIn.Store(true)
			before := calls.Load()
			confirmed := &toolContext{ctx: hitlCaller(t, true), confirmation: &toolconfirmation.ToolConfirmation{Confirmed: true}}
			result, err := whoami.Run(confirmed, map[string]any{})
			require.NoError(t, err)
			require.Equal(t, map[string]any{"output": "jane"}, result)
			require.Equal(t, before+1, calls.Load(), "the confirmed call runs once")

			rejected := &toolContext{ctx: hitlCaller(t, true), confirmation: &toolconfirmation.ToolConfirmation{Confirmed: false}}
			_, err = whoami.Run(rejected, map[string]any{})
			require.True(t, errors.Is(err, tool.ErrConfirmationRejected), "err = %v", err)
		})
	}
}

func TestElicitationHandlerRelaysOnlyAURLDuringAToolCall(t *testing.T) {
	handler := elicitationHandler("gateway")
	signIn := &mcpsdk.ElicitParams{Mode: "url", Message: "Sign in", URL: "https://gateway.example/s", ElicitationID: "e1"}
	form := &mcpsdk.ElicitParams{Mode: "form", Message: "Your token?", RequestedSchema: map[string]any{"type": "object"}}

	result, err := handler(t.Context(), &mcpsdk.ElicitRequest{Params: signIn})
	require.NoError(t, err)
	require.Equal(t, "decline", result.Action, "a URL outside a tool call is declined")

	recorder := &signInRecorder{}
	during := context.WithValue(t.Context(), signInRecorderKey{}, recorder)
	result, err = handler(during, &mcpsdk.ElicitRequest{Params: form})
	require.NoError(t, err)
	require.Equal(t, "decline", result.Action, "a form is declined")
	require.Nil(t, recorder.take())

	result, err = handler(during, &mcpsdk.ElicitRequest{Params: signIn})
	require.NoError(t, err)
	require.Equal(t, "cancel", result.Action, "the call ends without running")
	require.Equal(t, &SignIn{Server: "gateway", Message: "Sign in", URL: "https://gateway.example/s", ElicitationID: "e1"}, recorder.take())

	unsafe := &mcpsdk.ElicitParams{Mode: "url", Message: "Sign in", URL: "javascript:alert(1)", ElicitationID: "e2"}
	unsafeRecorder := &signInRecorder{}
	result, err = handler(context.WithValue(t.Context(), signInRecorderKey{}, unsafeRecorder), &mcpsdk.ElicitRequest{Params: unsafe})
	require.NoError(t, err)
	require.Equal(t, "decline", result.Action, "a URL that is not safe to show the person is declined")
	require.Nil(t, unsafeRecorder.take())
}

func TestSignInToolRefusesACallerWithoutTheHITLExtension(t *testing.T) {
	for _, test := range []struct {
		name      string
		stateless bool
	}{
		{name: "input required", stateless: true},
		{name: "-32042", stateless: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var signedIn atomic.Bool
			var calls atomic.Int32
			serverURL := signInServer(t, test.stateless, &signedIn, &calls)
			toolset, err := mcptoolset.New(mcptoolset.Config{
				Transport: &mcpsdk.StreamableClientTransport{Endpoint: serverURL},
				Client:    newElicitingClient("gateway"),
			})
			require.NoError(t, err)
			wrapped := &signInToolset{inner: toolset, server: "gateway"}

			for name, ctx := range map[string]context.Context{
				"not an A2A call":         t.Context(),
				"extension not activated": hitlCaller(t, false),
			} {
				toolCtx := &toolContext{ctx: ctx}
				tools, err := wrapped.Tools(toolCtx)
				require.NoError(t, err)
				_, err = tools[0].(*signInTool).Run(toolCtx, map[string]any{})
				require.Error(t, err, name)
				require.NotErrorIs(t, err, tool.ErrConfirmationRequired, name)
				require.Contains(t, err.Error(), `"gateway"`, name)
				require.NotContains(t, err.Error(), "https://gateway.example", name)
				require.NotContains(t, err.Error(), "Sign in to github", name, "the server's own text stays out of the model's context")
				require.Nil(t, toolCtx.payload, name)
			}
		})
	}
}

func TestServerDisplayNameIsTheConfiguredOne(t *testing.T) {
	for _, test := range []struct {
		params mcpServerParams
		want   string
	}{
		{params: mcpServerParams{Name: "github", URL: "https://gateway.example/mcp"}, want: "github"},
		{params: mcpServerParams{URL: "https://gateway.example/mcp"}, want: "gateway.example"},
		{params: mcpServerParams{URL: "./server", ServerType: "stdio"}, want: "./server"},
	} {
		require.Equal(t, test.want, test.params.displayName())
	}
}
