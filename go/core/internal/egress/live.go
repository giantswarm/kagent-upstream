package egress

import (
	"context"
	"net/url"
	"slices"
	"strings"
)

// GrantStore reads the EgressPolicy grants a runtime revision's active source
// holds now: an Agent's or a SandboxTemplate's, never a retired identity's. A
// missing revision or malformed stored grants are an error, never permission
// to keep old access.
type GrantStore interface {
	GetRuntimeEgressGrant(context.Context, string) (Access, error)
}

// EffectiveAccess is what a revision's actors may reach now: the revision's
// pinned base, its inferred model, tool and artifact access, plus the grants
// compatible with it (FilterGrants). The base always wins.
func EffectiveAccess(base, grants Access, caller *CallerCredential) (Access, error) {
	kept, _ := FilterGrants(base, grants, caller)
	destinations := slices.Clone(base.Destinations)
	for _, origin := range kept.Destinations {
		if !slices.Contains(destinations, origin) {
			destinations = append(destinations, origin)
		}
	}
	credentials, err := MergeCredentials(base.Credentials, kept.Credentials)
	if err != nil {
		return Access{}, err
	}
	return Access{Destinations: destinations, Credentials: credentials}, nil
}

// FilterGrants keeps the grants that fit a revision's base and returns the
// origins it dropped, sorted and once each. grants must be canonical on their
// own (CanonicalAccess). A grant is dropped when its origin takes another
// protocol on a host and port the base or an earlier grant holds, when the
// combined policy would exceed 256 rules or 16 headers on one host, when its
// credential binds a host, header and path prefix the base binds differently,
// and when its credential would replace the caller's token the runtime sends
// there (CallerCredential.ReservesCredential). A dropped credential keeps its
// destination when the destination itself fits.
func FilterGrants(base, grants Access, caller *CallerCredential) (Access, []string) {
	var kept Access
	var dropped []string
	protocols := map[string]string{}
	allowed := map[string]bool{}
	for _, origin := range base.Destinations {
		if u, err := url.Parse(origin); err == nil {
			protocols[u.Host] = u.Scheme
		}
		allowed[origin] = true
	}
	rules := len(allowed)
	for _, origin := range grants.Destinations {
		if allowed[origin] {
			kept.Destinations = append(kept.Destinations, origin)
			continue
		}
		u, err := url.Parse(origin)
		if err != nil {
			dropped = append(dropped, origin)
			continue
		}
		if scheme, exists := protocols[u.Host]; (exists && scheme != u.Scheme) || rules >= 256 {
			dropped = append(dropped, origin)
			continue
		}
		protocols[u.Host] = u.Scheme
		allowed[origin] = true
		rules++
		kept.Destinations = append(kept.Destinations, origin)
	}
	https := map[string]bool{}
	for origin := range allowed {
		if u, err := url.Parse(origin); err == nil && u.Scheme == "https" && slices.Contains(kept.Destinations, origin) {
			https[u.Hostname()] = true
		}
	}
	headers := map[string]int{}
	for _, binding := range base.Credentials {
		headers[binding.Hostname]++
	}
	for _, binding := range grants.Credentials {
		origin := "https://" + binding.Hostname
		conflicts := slices.ContainsFunc(base.Credentials, func(c Credential) bool {
			return c.Hostname == binding.Hostname && c.Header == binding.Header && c.PathPrefix == binding.PathPrefix
		})
		switch {
		case slices.Contains(base.Credentials, binding):
			kept.Credentials = append(kept.Credentials, binding)
		case !https[binding.Hostname] || conflicts || caller.ReservesCredential(binding.Hostname, binding.Header) || headers[binding.Hostname] >= 16:
			dropped = append(dropped, origin)
		default:
			headers[binding.Hostname]++
			kept.Credentials = append(kept.Credentials, binding)
		}
	}
	slices.Sort(dropped)
	return kept, slices.Compact(dropped)
}

// IntersectAccess keeps the destinations and complete credential bindings
// present in both a and b. a is canonical; b may be an invalid union of
// otherwise valid policies, and a subset of a valid a stays valid.
func IntersectAccess(a, b Access) Access {
	var result Access
	for _, origin := range a.Destinations {
		if slices.Contains(b.Destinations, origin) {
			result.Destinations = append(result.Destinations, origin)
		}
	}
	for _, binding := range a.Credentials {
		if slices.Contains(b.Credentials, binding) && slices.ContainsFunc(result.Destinations, func(origin string) bool {
			u, err := url.Parse(origin)
			return err == nil && u.Scheme == "https" && strings.EqualFold(u.Hostname(), binding.Hostname)
		}) {
			result.Credentials = append(result.Credentials, binding)
		}
	}
	return result
}
