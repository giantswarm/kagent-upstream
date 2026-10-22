package egress

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFilterGrants(t *testing.T) {
	open := &CallerCredential{PassthroughRecorded: true}
	ticket := Credential{Hostname: "tickets.example.com", Header: "authorization", Prefix: "Bearer ", URI: CredentialURI(KubernetesSecretAuthority, "team-a", "tickets", "token")}
	grants := Access{Destinations: []string{"https://tickets.example.com:443"}, Credentials: []Credential{ticket}}
	model := Credential{Hostname: "tickets.example.com", Header: "authorization", URI: CredentialURI(KubernetesSecretAuthority, "team-a", "model", "key")}

	for _, tc := range []struct {
		name    string
		base    Access
		caller  *CallerCredential
		kept    Access
		dropped []string
	}{
		{name: "fits", caller: open, kept: grants},
		{name: "already in the base", base: grants, caller: open, kept: grants},
		{
			name:    "another protocol on the same host and port",
			base:    Access{Destinations: []string{"tls://tickets.example.com:443"}},
			caller:  open,
			dropped: []string{"https://tickets.example.com", "https://tickets.example.com:443"},
		},
		{
			name:    "a credential the base binds differently keeps its destination",
			base:    Access{Destinations: []string{"https://tickets.example.com:443"}, Credentials: []Credential{model}},
			caller:  open,
			kept:    Access{Destinations: grants.Destinations},
			dropped: []string{"https://tickets.example.com"},
		},
		{
			name:    "a host taking the caller's token",
			caller:  &CallerCredential{Hosts: []string{"tickets.example.com"}, PassthroughRecorded: true},
			kept:    Access{Destinations: grants.Destinations},
			dropped: []string{"https://tickets.example.com"},
		},
		{
			name:    "a revision without its caller credential fails closed",
			kept:    Access{Destinations: grants.Destinations},
			dropped: []string{"https://tickets.example.com"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := FilterGrants(tc.base, grants, tc.caller)
			require.Equal(t, tc.kept, kept)
			require.Equal(t, tc.dropped, dropped)
		})
	}
}

func TestFilterGrantsHoldsTheRuleLimit(t *testing.T) {
	var base, grants Access
	for i := range 255 {
		base.Destinations = append(base.Destinations, fmt.Sprintf("https://host-%03d.example.com:443", i))
	}
	grants.Destinations = []string{"https://a.example.com:443", "https://b.example.com:443"}
	kept, dropped := FilterGrants(base, grants, &CallerCredential{PassthroughRecorded: true})
	require.Equal(t, []string{"https://a.example.com:443"}, kept.Destinations)
	require.Equal(t, []string{"https://b.example.com:443"}, dropped)
}

func TestEffectiveAccessKeepsTheBase(t *testing.T) {
	base := Access{
		Destinations: []string{"https://models.example.com:443"},
		Credentials:  []Credential{{Hostname: "models.example.com", Header: "x-api-key", URI: CredentialURI(KubernetesSecretAuthority, "team-a", "model", "key")}},
	}
	grants := Access{Destinations: []string{"https://models.example.com:443", "https://tickets.example.com:443"}}
	access, err := EffectiveAccess(base, grants, &CallerCredential{PassthroughRecorded: true})
	require.NoError(t, err)
	require.Equal(t, []string{"https://models.example.com:443", "https://tickets.example.com:443"}, access.Destinations)
	require.Equal(t, base.Credentials, access.Credentials)

	access, err = EffectiveAccess(base, Access{}, nil)
	require.NoError(t, err)
	require.Equal(t, base, access, "revoking every grant leaves the base")
}

func TestIntersectAccess(t *testing.T) {
	ticket := Credential{Hostname: "tickets.example.com", Header: "authorization", URI: CredentialURI(KubernetesSecretAuthority, "team-a", "tickets", "token")}
	previous := Access{Destinations: []string{"https://docs.example.com:443", "https://tickets.example.com:443"}, Credentials: []Credential{ticket}}
	requested := Access{Destinations: []string{"https://docs.example.com:443", "https://new.example.com:443"}, Credentials: []Credential{ticket}}
	require.Equal(t, Access{Destinations: []string{"https://docs.example.com:443"}}, IntersectAccess(previous, requested),
		"a credential whose destination left goes with it; an addition waits")
}

func TestReservesCredential(t *testing.T) {
	caller := &CallerCredential{Hosts: []string{"mcp.example.com"}, PassthroughHosts: []string{"llm.example.com"}, Always: true, PassthroughRecorded: true}
	require.True(t, caller.ReservesCredential("LLM.example.com.", "x-api-key"))
	require.True(t, caller.ReservesCredential("mcp.example.com", "Authorization"))
	require.False(t, caller.ReservesCredential("mcp.example.com", "x-api-key"))
	require.False(t, caller.ReservesCredential("other.example.com", "authorization"))
	require.True(t, (&CallerCredential{Always: true}).ReservesCredential("other.example.com", "x-api-key"),
		"a revision recorded before its passthrough hosts cannot tell them from an STS exchange")
	var missing *CallerCredential
	require.True(t, missing.ReservesCredential("other.example.com", "x-api-key"))
}
