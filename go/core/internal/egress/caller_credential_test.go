package egress

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallerCredentialAddsEachHostOnce(t *testing.T) {
	var credential CallerCredential
	for _, rawURL := range []string{"https://Muster.example.svc./mcp", "http://mcp.example.com:8080/a", "https://muster.example.svc/b", "not a url\x7f", "/relative"} {
		credential.Add(rawURL)
	}
	require.Equal(t, []string{"mcp.example.com", "muster.example.svc"}, credential.Hosts)
}

func TestCallerCredentialNeeded(t *testing.T) {
	muster := []string{"muster.example.svc"}
	for _, test := range []struct {
		name          string
		credential    CallerCredential
		actorJWTHosts []string
		want          bool
	}{
		{name: "taken nowhere", actorJWTHosts: muster},
		{name: "every host takes the actor JWT", credential: CallerCredential{Hosts: muster}, actorJWTHosts: muster},
		{name: "a host outside them", credential: CallerCredential{Hosts: []string{"mcp.example.com", "muster.example.svc"}}, actorJWTHosts: muster, want: true},
		{name: "no actor-JWT hosts", credential: CallerCredential{Hosts: muster}, want: true},
		{name: "a use outside any host", credential: CallerCredential{Always: true}, actorJWTHosts: muster, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, test.credential.Needed(test.actorJWTHosts))
		})
	}
}
