package callercredential

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestBroker(t *testing.T, handler http.HandlerFunc) *Broker {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	secret := filepath.Join(t.TempDir(), "client-secret")
	require.NoError(t, os.WriteFile(secret, []byte("s3cr:t\n"), 0o600))
	return &Broker{TokenURL: server.URL + "/oauth/token", ClientID: "kagent-caller", ClientSecretFile: secret, HTTPClient: server.Client()}
}

func TestBrokerExchangesTheCallersTokenForTheAudience(t *testing.T) {
	broker := newTestBroker(t, func(response http.ResponseWriter, request *http.Request) {
		id, secret, ok := request.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "kagent-caller", id)
		require.Equal(t, "s3cr%3At", secret, "the secret is form-encoded before Basic and its newline dropped")
		require.NoError(t, request.ParseForm())
		require.Equal(t, tokenExchangeGrantType, request.PostForm.Get("grant_type"))
		require.Equal(t, "caller-id-token", request.PostForm.Get("subject_token"))
		require.Equal(t, IDTokenType, request.PostForm.Get("subject_token_type"))
		require.Equal(t, "github", request.PostForm.Get("audience"))
		require.Equal(t, accessTokenType, request.PostForm.Get("requested_token_type"))
		require.Empty(t, request.PostForm.Get("resource"))
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"access_token":"ghu_person","issued_token_type":"urn:ietf:params:oauth:token-type:access_token","token_type":"Bearer","expires_in":600}`))
	})
	now := time.Now()
	token, expires, err := broker.Exchange(t.Context(), "caller-id-token", "github", now)
	require.NoError(t, err)
	require.Equal(t, "ghu_person", token)
	require.Equal(t, now.Add(10*time.Minute), expires)
}

func TestBrokerRefusalsTellTheCallerFromTheBroker(t *testing.T) {
	answer := func(code int, body string) http.HandlerFunc {
		return func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(code)
			_, _ = response.Write([]byte(body))
		}
	}
	_, _, err := newTestBroker(t, answer(http.StatusBadRequest, `{"error":"invalid_target","error_description":"no grant"}`)).Exchange(t.Context(), "s", "github", time.Now())
	require.ErrorIs(t, err, ErrNoGrant)
	_, _, err = newTestBroker(t, answer(http.StatusBadRequest, `{"error":"invalid_grant"}`)).Exchange(t.Context(), "s", "github", time.Now())
	require.ErrorIs(t, err, ErrNoGrant)
	_, _, err = newTestBroker(t, answer(http.StatusUnauthorized, `{"error":"invalid_client"}`)).Exchange(t.Context(), "s", "github", time.Now())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrNoGrant, "a misconfigured client is the platform's fault, not the caller's")
	_, _, err = newTestBroker(t, answer(http.StatusOK, `{}`)).Exchange(t.Context(), "s", "github", time.Now())
	require.ErrorContains(t, err, "no access_token")
}
