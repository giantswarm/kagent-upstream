package a2agateway

import (
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1/pbconv"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
)

const metadataDiscoveryTextMode = "text/plain"

func TestGatewayPreservesRequestMetadataCapability(t *testing.T) {
	session := gatewayTestSession()
	source, err := pbconv.ToProtoAgentCard(&a2atype.AgentCard{
		Name: session.GetAgent().GetName(), Description: "Metadata-capable agent", Version: "v1",
		SupportedInterfaces: []*a2atype.AgentInterface{a2atype.NewAgentInterface("http://127.0.0.1:80", a2atype.TransportProtocolGRPC)},
		DefaultInputModes:   []string{metadataDiscoveryTextMode}, DefaultOutputModes: []string{metadataDiscoveryTextMode}, Skills: []a2atype.AgentSkill{},
		Capabilities: a2atype.AgentCapabilities{Extensions: []a2atype.AgentExtension{apia2a.HITLExtension(), apia2a.MCPRequestMetadataExtension()}},
	})
	require.NoError(t, err)
	original := proto.Clone(source)
	store := &gatewayTestStore{revision: &database.RuntimeRevision{Revision: session.GetPreparedRevision(), AgentCard: source}}
	dialer := &gatewayTestDialer{}
	gateway := newTestGateway(store, &gatewayTestAuthorizer{}, dialer, gatewayTestURL)
	ctx := a2atype.AttachTenant(auth.AuthSessionTo(t.Context(), gatewayTestAuthSession{}), "team-a/assistant")
	card, err := gateway.GetExtendedAgentCard(ctx, &a2atype.GetExtendedAgentCardRequest{})
	require.NoError(t, err)
	var extensionURIs []string
	for _, extension := range card.Capabilities.Extensions {
		extensionURIs = append(extensionURIs, extension.URI)
		require.False(t, extension.Required)
	}
	require.ElementsMatch(t, []string{apia2a.MCPRequestMetadataExtensionURI, apia2a.HITLExtensionURI}, extensionURIs)
	require.True(t, proto.Equal(original, source))
	require.Nil(t, dialer.session)
}
