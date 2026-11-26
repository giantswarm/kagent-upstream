package a2a

import (
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/stretchr/testify/require"
)

func TestHITLAndMCPMetadataActivation(t *testing.T) {
	for name, headerValues := range map[string][]string{
		"joined":   {HITLExtensionURI + ", " + apia2a.MCPToolCallMetadataExtensionURI},
		"repeated": {HITLExtensionURI, apia2a.MCPToolCallMetadataExtensionURI},
		"spaces":   {" " + apia2a.MCPToolCallMetadataExtensionURI + " ,\t" + HITLExtensionURI + " "},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, callContext := a2asrv.NewCallContext(t.Context(), a2asrv.NewServiceParams(map[string][]string{
				a2atype.SvcParamExtensions: headerValues,
			}))
			request := &a2asrv.Request{Payload: &a2atype.SendMessageRequest{}}
			_, _, err := HITLActivationInterceptor().Before(ctx, callContext, request)
			require.NoError(t, err)
			_, _, err = MCPToolCallMetadataActivationInterceptor().Before(ctx, callContext, request)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{HITLExtensionURI, apia2a.MCPToolCallMetadataExtensionURI}, callContext.Extensions().ActivatedURIs())
			require.True(t, apia2a.HITLActive(ctx))
		})
	}
}
