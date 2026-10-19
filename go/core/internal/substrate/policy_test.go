package substrate

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
)

func TestActorPolicyKeepsCredentialsOnExactOrigin(t *testing.T) {
	policy, err := ActorEgressPolicy("team", []string{"https://*.example.com", "https://api.example.com:443", "https://api.example.com:8443", "http://api.example.com"}, []egress.Credential{
		{Origin: "https://api.example.com", Header: "authorization", Prefix: "Bearer ", URI: "ate-secret://k8s.io/default/team/auth/token"},
		{Origin: "https://api.example.com:8443", Header: "x-api-key", URI: "ate-secret://k8s.io/default/team/other/key"},
	})
	require.NoError(t, err)
	require.Len(t, policy.Rules, 4)
	require.Empty(t, policy.Rules[0].GetHttp().GetEffects().GetReplaceHeaders())
	require.Equal(t, []string{"*.example.com"}, policy.Rules[1].GetHttps().GetHostnames())
	require.Empty(t, policy.Rules[1].GetHttps().GetEffects().GetReplaceHeaders())
	for i, header := range []string{"authorization", "x-api-key"} {
		rule := policy.Rules[i+2].GetHttps()
		require.Equal(t, []string{"api.example.com"}, rule.GetHostnames())
		require.Len(t, rule.GetEffects().GetReplaceHeaders(), 1)
		require.Equal(t, header, rule.GetEffects().GetReplaceHeaders()[0].Header)
	}
	require.Equal(t, []int32{443}, policy.Rules[2].GetHttps().GetPorts().GetNumbers())
	require.Equal(t, []int32{8443}, policy.Rules[3].GetHttps().GetPorts().GetNumbers())
}

func sessionCredential(origin, header, uri string) *apiv1.SessionEgressDestination {
	return &apiv1.SessionEgressDestination{Origin: origin, Authentication: &apiv1.SessionEgressAuthentication{
		Header: header, Prefix: "Bearer ", Source: &apiv1.SessionEgressAuthentication_CredentialUri{CredentialUri: uri},
	}}
}

// A session's egress adds its origins over HTTPS and its headers beside the
// Agent's own, from a credential URI or the actor's JWT, and never takes over a
// header the Agent's policy sets.
func TestWithSessionEgress(t *testing.T) {
	base, err := ActorEgressPolicy("team", []string{"https://api.example.com", "https://*.example.com", "http://plain.example.com"}, []egress.Credential{
		{Origin: "https://api.example.com", Header: "authorization", Prefix: "Bearer ", URI: "ate-secret://k8s.io/default/team/auth/token"},
	})
	require.NoError(t, err)
	before := proto.CloneOf(base)

	policy, err := WithSessionEgress(base, []*apiv1.SessionEgressDestination{
		sessionCredential("https://git.example.com:443", "authorization", "ate-secret://credentials.example.com/workspaces/git"),
		{Origin: "https://api.example.com:443", Authentication: &apiv1.SessionEgressAuthentication{
			Header: "X-Actor", Source: &apiv1.SessionEgressAuthentication_ActorJwt{ActorJwt: &apiv1.SessionActorJWT{Audiences: []string{"api"}}},
		}},
		{Origin: "https://downloads.example.com:443"},
	})
	require.NoError(t, err)
	require.True(t, proto.Equal(before, base), "the Agent's policy is not modified")
	require.Len(t, policy.Rules, len(base.Rules)+2)

	api := policy.Rules[2].GetHttps()
	require.Equal(t, []string{"api.example.com"}, api.GetHostnames())
	require.Len(t, api.GetEffects().GetReplaceHeaders(), 2)
	actor := api.GetEffects().GetReplaceHeaders()[1]
	require.Equal(t, "x-actor", actor.GetHeader())
	require.Equal(t, []string{"api"}, actor.GetActorJwt().GetAudiences())
	require.EqualValues(t, sessionActorJWTLifetime, actor.GetActorJwt().GetExpirationSeconds())

	git := policy.Rules[3].GetHttps()
	require.Equal(t, []string{"git.example.com"}, git.GetHostnames())
	require.Equal(t, []int32{443}, git.GetPorts().GetNumbers())
	require.Equal(t, "ate-secret://credentials.example.com/workspaces/git", git.GetEffects().GetReplaceHeaders()[0].GetCredentialUri())
	require.Equal(t, "Bearer ", git.GetEffects().GetReplaceHeaders()[0].GetPrefix())
	require.Equal(t, []string{"downloads.example.com"}, policy.Rules[4].GetHttps().GetHostnames())
	require.Empty(t, policy.Rules[4].GetHttps().GetEffects().GetReplaceHeaders())

	unchanged, err := WithSessionEgress(base, nil)
	require.NoError(t, err)
	require.Same(t, base, unchanged)

	for name, session := range map[string]*apiv1.SessionEgressDestination{
		"the Agent's header":      sessionCredential("https://api.example.com:443", "Authorization", "ate-secret://credentials.example.com/workspaces/api"),
		"plain http on that port": {Origin: "https://plain.example.com:80"},
		"a wildcard":              {Origin: "https://*.example.com:443"},
		"plain http":              {Origin: "http://git.example.com:80"},
		"no source":               {Origin: "https://git.example.com:443", Authentication: &apiv1.SessionEgressAuthentication{Header: "authorization"}},
		"a routing header":        sessionCredential("https://git.example.com:443", "host", "ate-secret://credentials.example.com/workspaces/git"),
	} {
		_, err := WithSessionEgress(base, []*apiv1.SessionEgressDestination{session})
		require.Error(t, err, name)
	}
}
