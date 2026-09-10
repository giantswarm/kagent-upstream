package app

import (
	"fmt"
	"os"

	authimpl "github.com/kagent-dev/kagent/go/core/internal/httpserver/auth"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
)

// The authentication modes AUTH_MODE selects between. The Helm chart renders
// the variable from controller.auth.mode, so the mode is a deployment setting
// rather than a rebuild.
const (
	// AuthModeUnsecure takes the caller's identity from the X-User-Id header or
	// the user_id query parameter and falls back to a fixed admin identity. It
	// is the default, and it is only safe where nothing but trusted components
	// can reach the controller.
	AuthModeUnsecure = "unsecure"
	// AuthModeTrustedProxy takes the caller's identity from the bearer token in
	// the Authorization header: the claim AUTH_USER_ID_CLAIM names, "sub" by
	// default. The token is decoded, not verified — the proxy in front of the
	// controller (oauth2-proxy, an API gateway) has validated it, so that proxy
	// must remain the only way to reach the controller. A request without a
	// bearer is refused, whatever X-User-Id says.
	AuthModeTrustedProxy = "trusted-proxy"
)

// AuthenticatorFromEnv returns the built-in authenticator AUTH_MODE names.
//
// An unset AUTH_MODE selects AuthModeUnsecure, the same authenticator Run
// falls back to when Options.Authenticator is nil. An unknown mode is an error
// rather than a fallback: a controller that was configured to trust its proxy
// must not come up admitting everyone because of a typo.
func AuthenticatorFromEnv() (auth.AuthProvider, error) {
	return authenticatorForMode(os.Getenv("AUTH_MODE"), os.Getenv("AUTH_USER_ID_CLAIM"))
}

func authenticatorForMode(mode, userIDClaim string) (auth.AuthProvider, error) {
	switch mode {
	case "", AuthModeUnsecure:
		return &authimpl.UnsecureAuthenticator{}, nil
	case AuthModeTrustedProxy:
		return authimpl.NewProxyAuthenticator(userIDClaim), nil
	default:
		return nil, fmt.Errorf("unknown AUTH_MODE %q (valid modes: %s, %s)", mode, AuthModeUnsecure, AuthModeTrustedProxy)
	}
}
