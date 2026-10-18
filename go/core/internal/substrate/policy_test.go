package substrate

import (
	"testing"

	"github.com/stretchr/testify/require"

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

func TestActorPolicyCompilesTLSPassthrough(t *testing.T) {
	policy, err := ActorEgressPolicy("team", []string{"tls://db.example.com:5432", "tls://*.example.com:443", "https://api.example.com"}, nil)
	require.NoError(t, err)
	require.Len(t, policy.Rules, 3)
	require.Equal(t, []string{"api.example.com"}, policy.Rules[0].GetHttps().GetHostnames())
	for i, want := range []struct {
		host string
		port int32
	}{{"*.example.com", 443}, {"db.example.com", 5432}} {
		rule := policy.Rules[i+1]
		require.Nil(t, rule.GetHttp())
		require.Nil(t, rule.GetHttps())
		require.Equal(t, []string{want.host}, rule.GetTlsPassthrough().GetHostnames())
		require.Equal(t, []int32{want.port}, rule.GetTlsPassthrough().GetPorts().GetNumbers())
	}

	changed, err := ActorEgressPolicy("team", []string{"tls://db.example.com:6432"}, nil)
	require.NoError(t, err)
	require.Equal(t, []int32{6432}, changed.Rules[0].GetTlsPassthrough().GetPorts().GetNumbers())

	_, err = ActorEgressPolicy("team", []string{"tls://db.example.com:443", "https://db.example.com"}, nil)
	require.ErrorContains(t, err, "both HTTPS and TLS")
	_, err = ActorEgressPolicy("team", []string{"tls://db.example.com:443"}, []egress.Credential{{Origin: "tls://db.example.com:443", Header: "authorization", URI: "ate-secret://k8s.io/default/team/auth/token"}})
	require.Error(t, err)
}
