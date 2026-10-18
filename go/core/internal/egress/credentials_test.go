package egress

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalCredentials(t *testing.T) {
	base := Credential{Hostname: "API.Example.com.", Header: "Authorization", Prefix: "Bearer ", URI: "ate-secret://k8s.io/default/team/auth/token"}
	got, err := CanonicalCredentials([]Credential{base, base})
	require.NoError(t, err)
	require.Equal(t, []Credential{{Hostname: "api.example.com", Header: "authorization", Prefix: "Bearer ", URI: base.URI}}, got)
	require.Equal(t, "API.Example.com.", base.Hostname)
	for _, test := range []struct {
		name   string
		change func(*Credential)
	}{
		{"wildcard", func(c *Credential) { c.Hostname = "*.example.com" }},
		{"IP", func(c *Credential) { c.Hostname = "192.0.2.1" }},
		{"header", func(c *Credential) { c.Header = "Authorization\r\nInjected" }},
		{"prefix", func(c *Credential) { c.Prefix = "Bearer\n" }},
		{"provider", func(c *Credential) { c.URI = "https://example.com/secret" }},
		{"missing key", func(c *Credential) { c.URI = "ate-secret://k8s.io/default/team/auth" }},
		{"old provider", func(c *Credential) { c.URI = "ate-secret://kubernetes.io/team/auth/token" }},
		{"missing locator", func(c *Credential) { c.URI = "ate-secret://k8s.io/team/auth/token" }},
		{"unsupported locator", func(c *Credential) { c.URI = "ate-secret://k8s.io/remote/team/auth/token" }},
		{"query", func(c *Credential) { c.URI += "?key=other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			binding := base
			test.change(&binding)
			_, err := CanonicalCredentials([]Credential{binding})
			require.Error(t, err)
		})
	}
	other := base
	other.URI = "ate-secret://k8s.io/default/team/other/token"
	_, err = CanonicalCredentials([]Credential{base, other})
	require.ErrorContains(t, err, "conflicting credentials")
}

func TestParseActorJWTProviders(t *testing.T) {
	providers, err := ParseActorJWTProviders(`{"muster":{"hostname":"muster.agent-platform.svc","audience":"https://muster.example.com"}}`)
	require.NoError(t, err)
	require.Equal(t, map[string]ActorJWTProvider{"muster": {Hostname: "muster.agent-platform.svc", Audience: "https://muster.example.com"}}, providers)
	providers, err = ParseActorJWTProviders("")
	require.NoError(t, err)
	require.Empty(t, providers)
	for _, invalid := range []string{
		"muster=aud",
		`{"muster":{"hostname":"muster.svc"}}`,
		`{"muster":{"audience":"aud"}}`,
		`{"Muster":{"hostname":"muster.svc","audience":"aud"}}`,
		`{"muster":{"hostname":"10.0.0.1","audience":"aud"}}`,
		`{"muster":{"hostname":"muster.svc","audience":"aud","token":"x"}}`,
		`{"muster":{"hostname":"muster.svc","audience":"` + strings.Repeat("a", 513) + `"}}`,
	} {
		_, err := ParseActorJWTProviders(invalid)
		require.Error(t, err, "%q", invalid)
	}
}
