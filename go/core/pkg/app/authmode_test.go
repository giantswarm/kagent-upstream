package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	authimpl "github.com/kagent-dev/kagent/go/core/internal/httpserver/auth"
)

// unsignedJWT builds a token with the given claims and a signature nothing
// checks: the trusted-proxy authenticator decodes the payload without verifying.
func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	return encode([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + encode(payload) + "." + encode([]byte("signature"))
}

func TestAuthenticatorFromEnv(t *testing.T) {
	// The bearer names alice both ways; X-User-Id names someone else, which is
	// what tells the two modes apart.
	bearer := "Bearer " + unsignedJWT(t, map[string]any{"sub": "alice", "email": "alice@example.com"})

	tests := []struct {
		name        string
		mode        string
		userIDClaim string
		headers     http.Header
		wantType    string
		wantUserID  string
		wantErr     string // substring of the startup error
		wantAuthErr bool   // Authenticate refuses the request
	}{
		{
			name:       "unset selects unsecure and trusts X-User-Id",
			headers:    http.Header{"X-User-Id": {"mallory"}},
			wantType:   fmt.Sprintf("%T", &authimpl.UnsecureAuthenticator{}),
			wantUserID: "mallory",
		},
		{
			name:       "unsecure trusts X-User-Id",
			mode:       AuthModeUnsecure,
			headers:    http.Header{"X-User-Id": {"mallory"}, "Authorization": {bearer}},
			wantType:   fmt.Sprintf("%T", &authimpl.UnsecureAuthenticator{}),
			wantUserID: "mallory",
		},
		{
			name:       "trusted-proxy reads sub by default and ignores X-User-Id",
			mode:       AuthModeTrustedProxy,
			headers:    http.Header{"X-User-Id": {"mallory"}, "Authorization": {bearer}},
			wantType:   fmt.Sprintf("%T", &authimpl.ProxyAuthenticator{}),
			wantUserID: "alice",
		},
		{
			name:        "trusted-proxy reads the claim AUTH_USER_ID_CLAIM names",
			mode:        AuthModeTrustedProxy,
			userIDClaim: "email",
			headers:     http.Header{"X-User-Id": {"mallory"}, "Authorization": {bearer}},
			wantType:    fmt.Sprintf("%T", &authimpl.ProxyAuthenticator{}),
			wantUserID:  "alice@example.com",
		},
		{
			name:        "trusted-proxy refuses a request without a bearer",
			mode:        AuthModeTrustedProxy,
			headers:     http.Header{"X-User-Id": {"mallory"}},
			wantType:    fmt.Sprintf("%T", &authimpl.ProxyAuthenticator{}),
			wantAuthErr: true,
		},
		{
			name:    "unknown mode fails startup instead of running unsecure",
			mode:    "oidc",
			wantErr: `unknown AUTH_MODE "oidc"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AUTH_MODE", test.mode)
			t.Setenv("AUTH_USER_ID_CLAIM", test.userIDClaim)

			authenticator, err := AuthenticatorFromEnv()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("AuthenticatorFromEnv() error = %v, want one containing %q", err, test.wantErr)
				}
				if authenticator != nil {
					t.Fatalf("AuthenticatorFromEnv() = %T alongside an error", authenticator)
				}
				return
			}
			if err != nil {
				t.Fatalf("AuthenticatorFromEnv(): %v", err)
			}
			if got := fmt.Sprintf("%T", authenticator); got != test.wantType {
				t.Fatalf("AuthenticatorFromEnv() = %s, want %s", got, test.wantType)
			}

			session, err := authenticator.Authenticate(context.Background(), test.headers, url.Values{})
			if test.wantAuthErr {
				if err == nil {
					t.Fatalf("Authenticate() admitted %v, want a refusal", session.Principal())
				}
				return
			}
			if err != nil {
				t.Fatalf("Authenticate(): %v", err)
			}
			if got := session.Principal().User.ID; got != test.wantUserID {
				t.Errorf("user = %q, want %q", got, test.wantUserID)
			}
		})
	}
}
