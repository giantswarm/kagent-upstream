package a2a

import (
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestRequestMetadataAcceptsCoreKeySyntaxAndOpaqueValues(t *testing.T) {
	for _, key := range []string{"", "session", "com.example/", "a/b", "com.ex-ample9/a-b_c.d", "mcp.example/session", "com.example.mcp/session", "com.mcpserver/session"} {
		t.Run(strings.ReplaceAll(key, "/", "_"), func(t *testing.T) {
			require.NoError(t, ValidateMCPRequestMetadata(map[string]any{key: map[string]any{"any nested key": true}}))
		})
	}
	require.NoError(t, ValidateMCPRequestMetadata(nil))
	require.NoError(t, ValidateMCPRequestMetadata(map[string]any{"com.example/value": nil}))
}

func TestRequestMetadataRejectsMalformedAndTransportOwnedKeys(t *testing.T) {
	for _, key := range []string{
		"/session", "com..example/session", "1example/session", "example-/session", "ex_ample/session", "com.example/-session", "com.example/session_", "com.example/a/b", "com.example/a b",
		mcpsdk.MetaKeyProtocolVersion, mcpsdk.MetaKeyClientInfo, mcpsdk.MetaKeyClientCapabilities, "progressToken",
		mcpLogLevelMetadataKey, mcpsdk.MetaKeyServerInfo, mcpsdk.MetaKeySubscriptionID, "traceparent", "tracestate", "baggage",
		"io.modelcontextprotocol/related-task", "io.modelcontextprotocol/ui", "IO.ModelContextProtocol/session", "io.MCP/session", "dev.mcp.example/session",
	} {
		t.Run(strings.ReplaceAll(key, "/", "_"), func(t *testing.T) {
			err := ValidateMCPRequestMetadata(map[string]any{key: "private-value"})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-value")
		})
	}
}
