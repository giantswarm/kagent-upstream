package egress

import (
	"net/url"
	"slices"
	"strings"
)

// CallerCredential names where a runtime revision sends the caller's
// credential, the Authorization a turn's caller presented: the hosts of the MCP
// servers that take it, and whether a use outside any one host needs it (a
// model call passing the caller's token through, an STS exchange). The zero
// value takes it nowhere.
type CallerCredential struct {
	// Hosts are the lower-case hostnames of the servers that take the
	// caller's credential, sorted and once each.
	Hosts []string `json:"hosts,omitempty"`
	// Always is set when the runtime needs the caller's credential whatever
	// the Session's actor-JWT hosts.
	Always bool `json:"always,omitempty"`
}

// CallerCredentialHost returns the lower-case hostname of rawURL, the form
// Hosts holds; empty for a URL without one.
func CallerCredentialHost(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
}

// Add records the host of rawURL; a URL without a host takes nothing.
func (c *CallerCredential) Add(rawURL string) {
	host := CallerCredentialHost(rawURL)
	if host == "" {
		return
	}
	if i, found := slices.BinarySearch(c.Hosts, host); !found {
		c.Hosts = slices.Insert(c.Hosts, i, host)
	}
}

// Needed reports whether a turn must carry the caller's credential to the
// runtime when the egress gateway hands the session's Actor its own JWT on
// actorJWTHosts: a runtime sends a placeholder there, never the caller's
// credential, so only a host outside them or a use outside any host needs it.
func (c CallerCredential) Needed(actorJWTHosts []string) bool {
	if c.Always {
		return true
	}
	for _, host := range c.Hosts {
		if !slices.Contains(actorJWTHosts, host) {
			return true
		}
	}
	return false
}
