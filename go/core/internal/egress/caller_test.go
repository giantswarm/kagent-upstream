package egress

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallerCredentialURIRoundTrips(t *testing.T) {
	for _, credential := range []CallerCredential{
		{Audience: "github", Scheme: CallerSchemeBearer},
		{Audience: "github", Scheme: CallerSchemeBasic, Username: "x-access-token"},
	} {
		parsed, err := ParseCallerCredentialURI(credential.URI())
		require.NoError(t, err)
		require.Equal(t, credential, parsed)
	}
	require.Equal(t, "ate-secret://kagent.dev/caller/github/basic/x-access-token",
		CallerCredential{Audience: "github", Scheme: CallerSchemeBasic, Username: "x-access-token"}.URI())
}

func TestParseCallerCredentialURIRefusesOtherShapes(t *testing.T) {
	for _, uri := range []string{
		"ate-secret://kubernetes.io/team/auth/token",
		"ate-secret://kagent.dev/caller/github",
		"ate-secret://kagent.dev/caller/github/bearer/extra",
		"ate-secret://kagent.dev/caller/github/basic",
		"ate-secret://kagent.dev/caller/GitHub/bearer",
		"ate-secret://kagent.dev/caller/github/digest",
		"ate-secret://kagent.dev/caller/github/basic/x:y",
		"ate-secret://kagent.dev/other/github/bearer",
	} {
		_, err := ParseCallerCredentialURI(uri)
		require.Error(t, err, uri)
	}
}

func TestCanonicalCredentialsAcceptsCallerCredentials(t *testing.T) {
	caller := Credential{Hostname: "GitHub.com", Header: "Authorization", Prefix: "Basic ", URI: CallerCredential{Audience: "github", Scheme: CallerSchemeBasic, Username: "x-access-token"}.URI()}
	got, err := CanonicalCredentials([]Credential{caller})
	require.NoError(t, err)
	require.Equal(t, "github.com", got[0].Hostname)

	static := Credential{Hostname: "github.com", Header: "authorization", Prefix: "Basic ", URI: "ate-secret://kubernetes.io/team/git/basic"}
	_, err = CanonicalCredentials([]Credential{caller, static})
	require.ErrorContains(t, err, "conflicting credentials", "a host takes the caller's credential or a static one")

	caller.URI = "ate-secret://kagent.dev/caller/github"
	_, err = CanonicalCredentials([]Credential{caller})
	require.Error(t, err)
}
