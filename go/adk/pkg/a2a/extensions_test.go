package a2a

import (
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/stretchr/testify/require"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
)

func TestHITLActivationWithUnrelatedExtensions(t *testing.T) {
	const unrelated = "https://example.com/extensions/other/v1"
	for name, headerValues := range map[string][]string{
		"joined":   {HITLExtensionURI + ", " + unrelated},
		"repeated": {HITLExtensionURI, unrelated},
		"spaces":   {" " + unrelated + " ,\t" + HITLExtensionURI + " "},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, callContext := a2asrv.NewCallContext(t.Context(), a2asrv.NewServiceParams(map[string][]string{
				a2atype.SvcParamExtensions: headerValues,
			}))
			request := &a2asrv.Request{Payload: &a2atype.SendMessageRequest{}}
			_, _, err := HITLActivationInterceptor().Before(ctx, callContext, request)
			require.NoError(t, err)
			require.ElementsMatch(t, []string{HITLExtensionURI}, callContext.Extensions().ActivatedURIs())
			require.True(t, apia2a.HITLActive(ctx))
		})
	}
}
