package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool/mcptoolset"
	"google.golang.org/adk/v2/tool/toolconfirmation"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
)

const (
	toolCallMetadataSessionKey    = "com.example/session"
	toolCallMetadataConfigKey     = "com.example/config"
	metadataLegacyProtocolVersion = "2025-11-25"
	metadataModernProtocolVersion = "2026-07-28"
	metadataTestToolName          = "echo"
	metadataTestServerTypeHTTP    = "http"
)

func TestToolCallMetadataNegotiatesAndForwardsPayload(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		t.Run(fmt.Sprintf("Given stateless=%t, when a tool is called, assert that the complete payload is sent with negotiated support", stateless), func(t *testing.T) {
			serverURL, received := metadataServer(t, stateless, map[string]any{
				apia2a.MCPToolCallMetadataExtensionID: map[string]any{},
			})
			call := metadataTool(t, serverURL)
			values := map[string]any{
				toolCallMetadataSessionKey: "a1",
				toolCallMetadataConfigKey:  map[string]any{"format": "csv", "limit": 10.0},
				"format":                   "csv",
			}
			require.NoError(t, call(t.Context(), values))
			record := <-received
			wantVersion := metadataLegacyProtocolVersion
			if stateless {
				wantVersion = metadataModernProtocolVersion
			}
			require.Equal(t, wantVersion, record.version)
			require.Equal(t, values, record.meta[apia2a.MCPToolCallMetadataExtensionID])
			require.NotNil(t, record.capabilities)
			require.Equal(t, map[string]any{}, record.capabilities.Extensions[apia2a.MCPToolCallMetadataExtensionID])
			require.NotNil(t, record.capabilities.Elicitation)
			require.NotContains(t, record.capabilities.Extensions, toolCallMetadataSessionKey)
			require.NotContains(t, record.meta, toolCallMetadataSessionKey)
			require.NotContains(t, record.meta, "format")
			require.Len(t, values, 3)
		})
	}
}

func TestToolCallMetadataRequiresServerSupport(t *testing.T) {
	for name, extensions := range map[string]map[string]any{
		"no extension":        nil,
		"unrelated extension": {toolCallMetadataSessionKey: map[string]any{}},
		"null settings":       {apia2a.MCPToolCallMetadataExtensionID: nil},
		"invalid settings":    {apia2a.MCPToolCallMetadataExtensionID: "invalid"},
	} {
		t.Run("Given "+name+", when a tool is called, assert that no metadata is forwarded", func(t *testing.T) {
			serverURL, received := metadataServer(t, true, extensions)
			call := metadataTool(t, serverURL)
			require.NoError(t, call(t.Context(), map[string]any{toolCallMetadataSessionKey: "private"}))
			require.NotContains(t, (<-received).meta, apia2a.MCPToolCallMetadataExtensionID)
		})
	}
}

func TestToolCallMetadataIsScopedToEachCall(t *testing.T) {
	serverURL, received := metadataServer(t, true, map[string]any{
		apia2a.MCPToolCallMetadataExtensionID: map[string]any{},
	})
	call := metadataTool(t, serverURL)
	for _, value := range []string{"first", "second", ""} {
		var values map[string]any
		if value != "" {
			values = map[string]any{toolCallMetadataSessionKey: value}
		}
		require.NoError(t, call(t.Context(), values))
		record := <-received
		if value == "" {
			require.NotContains(t, record.meta, apia2a.MCPToolCallMetadataExtensionID)
		} else {
			require.Equal(t, map[string]any{toolCallMetadataSessionKey: value}, record.meta[apia2a.MCPToolCallMetadataExtensionID])
		}
	}
}

func TestConcurrentToolCallsKeepTheirOwnMetadata(t *testing.T) {
	serverURL, received := metadataServer(t, true, map[string]any{
		apia2a.MCPToolCallMetadataExtensionID: map[string]any{},
	})
	call := metadataTool(t, serverURL)
	const calls = 12
	var workers sync.WaitGroup
	errors := make(chan error, calls)
	for index := range calls {
		workers.Go(func() {
			errors <- call(t.Context(), map[string]any{toolCallMetadataSessionKey: fmt.Sprintf("call-%d", index)})
		})
	}
	workers.Wait()
	seen := make(map[string]bool, calls)
	for range calls {
		require.NoError(t, <-errors)
		metadata, ok := (<-received).meta[apia2a.MCPToolCallMetadataExtensionID].(map[string]any)
		require.True(t, ok)
		value, ok := metadata[toolCallMetadataSessionKey].(string)
		require.True(t, ok)
		require.False(t, seen[value], "duplicate metadata %q", value)
		seen[value] = true
	}
	for index := range calls {
		require.True(t, seen[fmt.Sprintf("call-%d", index)])
	}
}

func TestToolCallMetadataPreservesExistingCallMetadata(t *testing.T) {
	serverURL, received := metadataServer(t, true, map[string]any{
		apia2a.MCPToolCallMetadataExtensionID: map[string]any{},
	})
	client := newElicitingClient("metadata")
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{Endpoint: serverURL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	metadata := mcpsdk.Meta{
		apia2a.MCPToolCallMetadataExtensionID: map[string]any{toolCallMetadataSessionKey: "existing"},
		"org.other/setting":                   true,
	}
	_, err = session.CallTool(WithToolCallMetadata(t.Context(), map[string]any{toolCallMetadataSessionKey: "replacement"}), &mcpsdk.CallToolParams{
		Name: metadataTestToolName, Meta: metadata,
	})
	require.NoError(t, err)
	record := <-received
	require.Equal(t, map[string]any{toolCallMetadataSessionKey: "existing"}, record.meta[apia2a.MCPToolCallMetadataExtensionID])
	require.Equal(t, true, record.meta["org.other/setting"])
	require.Equal(t, metadataModernProtocolVersion, record.meta[mcpsdk.MetaKeyProtocolVersion])
}

func TestToolDiscoveryAdvertisesToolCallMetadataCapability(t *testing.T) {
	capabilities := make(chan *mcpsdk.ClientCapabilities, 1)
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "metadata-discovery", Version: "1"}, nil)
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: metadataTestToolName}, func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, any, error) {
		return &mcpsdk.CallToolResult{}, nil, nil
	})
	server.AddReceivingMiddleware(func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, request mcpsdk.Request) (mcpsdk.Result, error) {
			if method == "tools/list" {
				list, ok := request.(*mcpsdk.ListToolsRequest)
				if !ok {
					return nil, fmt.Errorf("unexpected list request %T", request)
				}
				capabilities <- list.ClientCapabilities()
			}
			return next(ctx, method, request)
		}
	})
	httpServer := httptest.NewServer(mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, &mcpsdk.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(func() {
		httpServer.CloseClientConnections()
		httpServer.Close()
	})
	_, _, err := agentVisibleToolFilter(t.Context(), mcpServerParams{URL: httpServer.URL, ServerType: metadataTestServerTypeHTTP}, nil)
	require.NoError(t, err)
	require.Len(t, capabilities, 1)
	declared := <-capabilities
	require.NotNil(t, declared)
	require.Contains(t, declared.Extensions, apia2a.MCPToolCallMetadataExtensionID)
	require.Contains(t, declared.Extensions, mcpUIExtensionName)
}

type metadataRecord struct {
	meta         mcpsdk.Meta
	version      string
	capabilities *mcpsdk.ClientCapabilities
}

func metadataServer(t *testing.T, stateless bool, extensions map[string]any) (string, <-chan metadataRecord) {
	t.Helper()
	received := make(chan metadataRecord, 20)
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "metadata", Version: "1"}, &mcpsdk.ServerOptions{
		Capabilities: &mcpsdk.ServerCapabilities{Extensions: extensions},
	})
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: metadataTestToolName}, func(_ context.Context, request *mcpsdk.CallToolRequest, _ struct{}) (*mcpsdk.CallToolResult, any, error) {
		received <- metadataRecord{meta: request.Params.GetMeta(), version: request.ProtocolVersion(), capabilities: request.ClientCapabilities()}
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "ok"}}}, nil, nil
	})
	httpServer := httptest.NewServer(mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server { return server }, &mcpsdk.StreamableHTTPOptions{Stateless: stateless}))
	t.Cleanup(func() {
		httpServer.CloseClientConnections()
		httpServer.Close()
	})
	return httpServer.URL, received
}

func metadataTool(t *testing.T, serverURL string) func(context.Context, map[string]any) error {
	t.Helper()
	toolset, err := mcptoolset.New(mcptoolset.Config{
		Transport: &mcpsdk.StreamableClientTransport{Endpoint: serverURL},
		Client:    newElicitingClient("metadata"),
	})
	require.NoError(t, err)
	tools, err := toolset.Tools(metadataCallContext{ctx: t.Context()})
	require.NoError(t, err)
	require.Len(t, tools, 1)
	runnable, ok := tools[0].(interface {
		Run(adkagent.Context, any) (map[string]any, error)
	})
	require.True(t, ok)
	return func(ctx context.Context, values map[string]any) error {
		_, err := runnable.Run(metadataCallContext{ctx: WithToolCallMetadata(ctx, values)}, map[string]any{})
		return err
	}
}

type metadataCallContext struct {
	adkagent.Context
	ctx context.Context
}

func (c metadataCallContext) Deadline() (time.Time, bool)                          { return c.ctx.Deadline() }
func (c metadataCallContext) Done() <-chan struct{}                                { return c.ctx.Done() }
func (c metadataCallContext) Err() error                                           { return c.ctx.Err() }
func (c metadataCallContext) Value(key any) any                                    { return c.ctx.Value(key) }
func (c metadataCallContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }
