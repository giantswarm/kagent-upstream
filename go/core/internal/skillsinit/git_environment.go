package skillsinit

import (
	"net/url"
	"os"
	"strings"
)

// Environment variables a Harness may set to authenticate every git source on
// one host. The host is upper-cased with every character outside [A-Z0-9]
// replaced by '_': github.com becomes GITHUB_COM, git.example.com:8443
// becomes GIT_EXAMPLE_COM_8443.
const (
	GitTokenEnvPrefix    = "KAGENT_GIT_TOKEN_"
	GitUsernameEnvPrefix = "KAGENT_GIT_USERNAME_"
)

// GitCredentialFromEnvironment returns the Harness-provided credential for the
// URL's host, or nil when the environment names none. The token stays in its
// variable; only the variable's name is returned.
func GitCredentialFromEnvironment(rawURL string) *GitCredential {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return nil
	}
	suffix := hostEnvSuffix(parsed.Host)
	tokenEnv := GitTokenEnvPrefix + suffix
	if value, ok := os.LookupEnv(tokenEnv); !ok || value == "" {
		return nil
	}
	return &GitCredential{Username: os.Getenv(GitUsernameEnvPrefix + suffix), TokenEnv: tokenEnv}
}

func hostEnvSuffix(host string) string {
	var builder strings.Builder
	for _, r := range strings.ToUpper(host) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('_')
	}
	return builder.String()
}
