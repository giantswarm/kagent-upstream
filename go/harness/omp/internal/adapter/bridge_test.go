package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/harness/omp/internal/config"
	"github.com/kagent-dev/kagent/go/harness/omp/internal/driver"
)

const signInLink = "https://muster.example.com/oauth/proxy/start?state=s"

// upstream serves server over Streamable HTTP, as the binding's MCP server.
// Only a stateless server speaks MCP 2026-07-28, as muster does; a stateful
// one negotiates 2025-11-25 and can send notifications.
func upstream(t *testing.T, server *mcp.Server, stateless bool) string {
	t.Helper()
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: stateless})
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

// bridgeTo starts the bridge of one server and returns omp's session on it
// and the elicitations the bridge parks turns on.
func bridgeTo(t *testing.T, server config.MCPServer) (*mcp.ClientSession, <-chan *driver.Elicitation) {
	t.Helper()
	elicitations := make(chan *driver.Elicitation)
	urls, err := startBridges(t.Context(), []config.MCPServer{server}, &turnActorJWTHosts{}, elicitations)
	require.NoError(t, err)
	omp := mcp.NewClient(&mcp.Implementation{Name: "omp"}, nil)
	session, err := omp.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: urls[server.Name]}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session, elicitations
}

func text(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	content, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return content.Text
}

func answer(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func names[T any](t *testing.T, items func(context.Context) func(func(T, error) bool), name func(T) string) []string {
	t.Helper()
	var out []string
	for item, err := range items(t.Context()) {
		require.NoError(t, err)
		out = append(out, name(item))
	}
	slices.Sort(out)
	return out
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	return names(t, func(ctx context.Context) func(func(*mcp.Tool, error) bool) { return session.Tools(ctx, nil) },
		func(tool *mcp.Tool) string { return tool.Name })
}

func TestBridgeServesTheServersToolsResourcesAndPrompts(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "muster"}, nil)
	for _, name := range []string{"call_tool", "list_tools"} {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return answer(name + " ran"), nil
		})
	}
	server.AddResource(&mcp.Resource{URI: "muster://status", Name: "status"}, func(_ context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: request.Params.URI, Text: "ok"}}}, nil
	})
	server.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "muster://server/{name}", Name: "server"}, func(_ context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: request.Params.URI, Text: "a server"}}}, nil
	})
	server.AddPrompt(&mcp.Prompt{Name: "triage"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "triage it"}}}}, nil
	})
	session, _ := bridgeTo(t, config.MCPServer{Name: "muster", URL: upstream(t, server, true)})

	assert.Equal(t, []string{"call_tool", "list_tools"}, toolNames(t, session), "a binding that selects no tool gets every tool")
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_tools"})
	require.NoError(t, err)
	assert.Equal(t, "list_tools ran", text(t, result))

	read, err := session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "muster://status"})
	require.NoError(t, err)
	assert.Equal(t, "ok", read.Contents[0].Text)
	read, err = session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: "muster://server/github"})
	require.NoError(t, err)
	assert.Equal(t, "a server", read.Contents[0].Text, "a resource template's reads reach the server")
	prompt, err := session.GetPrompt(t.Context(), &mcp.GetPromptParams{Name: "triage"})
	require.NoError(t, err)
	assert.Equal(t, "triage it", prompt.Messages[0].Content.(*mcp.TextContent).Text)
}

func TestBridgeServesOnlyTheSelectedToolsAndFollowsListChanges(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "muster"}, nil)
	add := func(name string) {
		server.AddTool(&mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return answer(name), nil
		})
	}
	add("list")
	add("delete")
	session, _ := bridgeTo(t, config.MCPServer{Name: "muster", URL: upstream(t, server, false), Tools: []string{"list", "get"}})
	assert.Equal(t, []string{"list"}, toolNames(t, session))

	add("get")
	require.Eventually(t, func() bool { return slices.Equal(toolNames(t, session), []string{"get", "list"}) }, 5*time.Second, 50*time.Millisecond,
		"a selected tool the server lists later is served")
	server.RemoveTools("list")
	require.Eventually(t, func() bool { return slices.Equal(toolNames(t, session), []string{"get"}) }, 5*time.Second, 50*time.Millisecond,
		"a tool the server no longer lists is not served")
}

// signInServer asks for a sign-in on MCP 2026-07-28 until the call is
// retried with the person's answer.
func signInServer(answers chan<- string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "muster"}, nil)
	server.AddTool(&mcp.Tool{Name: "list_repos", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if response, ok := request.Params.InputResponses["sign_in"].(*mcp.ElicitResult); ok {
			answers <- response.Action
			if response.Action != apia2a.URLElicitationAccept {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "not run"}}}, nil
			}
			return answer("repos"), nil
		}
		return &mcp.CallToolResult{
			InputRequests: mcp.InputRequestMap{"sign_in": &mcp.ElicitParams{Mode: "url", Message: "Sign in to github to continue.", URL: signInLink}},
			RequestState:  "github",
		}, nil
	})
	return server
}

func TestBridgeParksTheTurnOnASignInAndRunsTheCallOnAccept(t *testing.T) {
	answers := make(chan string, 1)
	session, elicitations := bridgeTo(t, config.MCPServer{Name: "muster", URL: upstream(t, signInServer(answers), true), TimeoutSeconds: 0.5})

	called := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_repos"})
		assert.NoError(t, err)
		called <- result
	}()
	var elicitation *driver.Elicitation
	select {
	case elicitation = <-elicitations:
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge parked no turn on the sign-in")
	}
	request := elicitation.Request()
	assert.Equal(t, "muster", request.Server)
	assert.Equal(t, "Sign in to github to continue.", request.Message)
	assert.Equal(t, signInLink, request.URL)
	assert.NotEmpty(t, request.ID)

	// The person takes longer than the binding's timeout to sign in.
	time.Sleep(time.Second)
	elicitation.Answer(apia2a.URLElicitationAccept)
	assert.Equal(t, apia2a.URLElicitationAccept, <-answers)
	result := <-called
	assert.False(t, result.IsError)
	assert.Equal(t, "repos", text(t, result), "the call ran once the person accepted, past a timeout that stopped while they decided")
}

func TestBridgeTellsTheServerTheCallWasDeclined(t *testing.T) {
	answers := make(chan string, 1)
	session, elicitations := bridgeTo(t, config.MCPServer{Name: "muster", URL: upstream(t, signInServer(answers), true)})
	called := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_repos"})
		assert.NoError(t, err)
		called <- result
	}()
	(<-elicitations).Answer(apia2a.URLElicitationDecline)
	assert.Equal(t, apia2a.URLElicitationDecline, <-answers)
	assert.True(t, (<-called).IsError)
}

func TestBridgeAsksOnAURLElicitationRequiredError(t *testing.T) {
	var signedIn atomic.Bool
	server := mcp.NewServer(&mcp.Implementation{Name: "muster"}, nil)
	server.AddTool(&mcp.Tool{Name: "list_repos", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if signedIn.Load() {
			return answer("repos"), nil
		}
		return nil, mcp.URLElicitationRequiredError([]*mcp.ElicitParams{{Mode: "url", Message: "Sign in to github.", URL: signInLink, ElicitationID: "e1"}})
	})
	session, elicitations := bridgeTo(t, config.MCPServer{Name: "muster", URL: upstream(t, server, false)})

	called := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_repos"})
		assert.NoError(t, err)
		called <- result
	}()
	elicitation := <-elicitations
	assert.Equal(t, "e1", elicitation.Request().ElicitationID)
	signedIn.Store(true)
	elicitation.Answer(apia2a.URLElicitationAccept)
	assert.Equal(t, "repos", text(t, <-called), "the call ran again after the person accepted")

	signedIn.Store(false)
	go func() {
		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_repos"})
		assert.NoError(t, err)
		called <- result
	}()
	(<-elicitations).Answer(apia2a.URLElicitationDecline)
	declined := <-called
	assert.True(t, declined.IsError)
	assert.Contains(t, text(t, declined), "The tool did not run")
}

func TestBridgeDeclinesWhatIsNoSignInLink(t *testing.T) {
	for name, params := range map[string]*mcp.ElicitParams{
		"a form": {Mode: "form", Message: "Your token?", RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"token": map[string]any{"type": "string"}}}},
		"http":   {Mode: "url", Message: "Sign in.", URL: "http://muster.example.com/start"},
	} {
		t.Run(name, func(t *testing.T) {
			answers := make(chan string, 1)
			server := mcp.NewServer(&mcp.Implementation{Name: "muster"}, nil)
			server.AddTool(&mcp.Tool{Name: "ask", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if response, ok := request.Params.InputResponses["ask"].(*mcp.ElicitResult); ok {
					answers <- response.Action
					return answer(response.Action), nil
				}
				return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"ask": params}}, nil
			})
			session, elicitations := bridgeTo(t, config.MCPServer{Name: "muster", URL: upstream(t, server, true)})
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "ask"})
			require.NoError(t, err)
			assert.Equal(t, apia2a.URLElicitationDecline, <-answers)
			assert.Equal(t, apia2a.URLElicitationDecline, text(t, result))
			select {
			case <-elicitations:
				t.Fatal("the turn was parked on it")
			default:
			}
		})
	}
}
