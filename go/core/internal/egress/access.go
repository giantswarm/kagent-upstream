package egress

import (
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// Access is the resolved, revision-pinned network grant of EgressPolicies. It
// contains references, never Secret values.
type Access struct {
	Destinations []string     `json:"destinations,omitempty"`
	Credentials  []Credential `json:"credentials,omitempty"`
}

// CanonicalAccess merges equivalent origins and validates the complete grant
// before a revision can become ready: every credential binds an exact HTTPS
// destination of the same grant.
func CanonicalAccess(destinations []string, credentials []Credential) (Access, error) {
	var result Access
	var err error
	if result.Destinations, err = CanonicalDestinations(destinations); err != nil {
		return Access{}, err
	}
	https := map[string]bool{}
	for _, origin := range result.Destinations {
		if u, _ := url.Parse(origin); u.Scheme == "https" {
			https[u.Hostname()] = true
		}
	}
	if result.Credentials, err = CanonicalCredentials(credentials); err != nil {
		return Access{}, err
	}
	for _, binding := range result.Credentials {
		if !https[binding.Hostname] {
			return Access{}, fmt.Errorf("credential destination %q is not explicitly allowed over HTTPS", binding.Hostname)
		}
	}
	return result, nil
}

// CanonicalDestinations merges equivalent origins in sorted order and refuses
// two protocols on one hostname pattern and port, and more than 256 origins.
func CanonicalDestinations(destinations []string) ([]string, error) {
	var result []string
	protocols := map[string]string{}
	for _, raw := range destinations {
		origin, err := ParseOrigin(raw)
		if err != nil {
			return nil, err
		}
		// ParseOrigin returns scheme://host:port with a valid port; the port is
		// re-encoded so "0443" and "443" are one origin.
		u, _ := url.Parse(origin)
		port, _ := strconv.ParseUint(u.Port(), 10, 16)
		u.Host = net.JoinHostPort(u.Hostname(), strconv.FormatUint(port, 10))
		origin = u.String()
		if scheme, exists := protocols[u.Host]; exists && scheme != u.Scheme {
			return nil, ProtocolConflict(u.Host, scheme, u.Scheme)
		}
		protocols[u.Host] = u.Scheme
		if !slices.Contains(result, origin) {
			result = append(result, origin)
		}
	}
	slices.Sort(result)
	if len(result) > 256 {
		return nil, fmt.Errorf("egress policy exceeds 256 rules")
	}
	return result, nil
}

// ProtocolConflict refuses two protocols on one hostname pattern and port:
// Substrate selects its HTTP, HTTPS and TLS passthrough rules by the same
// pattern and port, so one pattern and port takes one protocol.
func ProtocolConflict(host, one, other string) error {
	schemes := []string{strings.ToUpper(one), strings.ToUpper(other)}
	slices.Sort(schemes)
	return fmt.Errorf("egress destination %q uses both %s and %s on the same port", host, schemes[0], schemes[1])
}

// ValidCredentialHeader reports whether a header may carry an injected
// credential: routing, framing, hop-by-hop and cookie headers may not.
func ValidCredentialHeader(header string) bool {
	if len(header) > 256 || !httpguts.ValidHeaderFieldName(header) {
		return false
	}
	switch strings.ToLower(header) {
	case "host", "connection", "content-length", "transfer-encoding", "te", "trailer", "upgrade", "keep-alive", "proxy-authorization", "proxy-authenticate", "cookie":
		return false
	}
	return true
}
