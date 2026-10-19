package substrate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"golang.org/x/net/http/httpguts"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
)

// ActorEgressPolicy compiles HTTP(S) origins into an actor's default allowlist.
// Credential bindings are already canonicalized by the store.
func ActorEgressPolicy(atespace string, destinations []string, credentials []egress.Credential) (*ateapipb.EgressPolicy, error) {
	access, err := egress.CanonicalAccess(destinations, credentials, nil)
	if err != nil {
		return nil, err
	}
	effects := map[string]*ateapipb.HttpRuleEffects{}
	for _, binding := range access.Credentials {
		if effects[binding.Origin] == nil {
			effects[binding.Origin] = &ateapipb.HttpRuleEffects{}
		}
		effects[binding.Origin].ReplaceHeaders = append(effects[binding.Origin].ReplaceHeaders, &ateapipb.CredentialHeader{Header: binding.Header, Prefix: binding.Prefix, CredentialUri: binding.URI})
	}
	policy := &ateapipb.EgressPolicy{Metadata: &ateapipb.ResourceMetadata{Atespace: atespace, Name: "default"}}
	for _, origin := range access.Destinations {
		target, _ := egress.ParseOrigin(origin) // validated by CanonicalAccess
		port, _ := strconv.ParseInt(target.Port(), 10, 32)
		ports := &ateapipb.Ports{Numbers: []int32{int32(port)}}
		// One rule per origin: exact matches win over wildcard rules, keeping
		// credentials on their exact origin even when a wildcard also grants access.
		if target.Scheme == "https" {
			policy.Rules = append(policy.Rules, &ateapipb.EgressRule{Https: &ateapipb.HTTPSRule{Hostnames: []string{target.Hostname()}, Ports: ports, Effects: effects[origin]}})
		} else {
			policy.Rules = append(policy.Rules, &ateapipb.EgressRule{Http: &ateapipb.HTTPRule{Hostnames: []string{target.Hostname()}, Ports: ports, Effects: effects[origin]}})
		}
	}
	return policy, nil
}

// sessionActorJWTLifetime is how long each actor JWT the gateway mints for a
// session's egress is valid: Substrate requires between 5 minutes and an hour.
const sessionActorJWTLifetime = 10 * 60

// WithSessionEgress adds a session's own egress to its actor's policy: each
// origin is allowed over HTTPS, and its authentication becomes a header the
// gateway replaces with the credential its source names. A session header on
// an origin and header the policy already sets is refused: the session never
// takes over a credential its Agent binds. The policy is not modified.
func WithSessionEgress(policy *ateapipb.EgressPolicy, session []*apiv1.SessionEgressDestination) (*ateapipb.EgressPolicy, error) {
	if len(session) == 0 {
		return policy, nil
	}
	result := proto.CloneOf(policy)
	for _, destination := range session {
		target, err := egress.ParseOrigin(destination.GetOrigin())
		if err != nil || target.Scheme != "https" || strings.HasPrefix(target.Hostname(), "*.") {
			return nil, fmt.Errorf("session egress needs an exact https origin, got %q", destination.GetOrigin())
		}
		port, _ := strconv.ParseInt(target.Port(), 10, 32)
		rule, err := exactHTTPSRule(result, target.Hostname(), int32(port))
		if err != nil {
			return nil, err
		}
		authentication := destination.GetAuthentication()
		if authentication == nil {
			continue
		}
		header, err := sessionCredentialHeader(authentication)
		if err != nil {
			return nil, fmt.Errorf("session egress to %s: %w", destination.GetOrigin(), err)
		}
		if rule.Effects == nil {
			rule.Effects = &ateapipb.HttpRuleEffects{}
		}
		for _, existing := range rule.Effects.ReplaceHeaders {
			if strings.EqualFold(existing.GetHeader(), header.GetHeader()) {
				return nil, fmt.Errorf("session egress to %s sets header %s, which its actor's policy already sets", destination.GetOrigin(), header.GetHeader())
			}
		}
		if len(rule.Effects.ReplaceHeaders) >= 16 {
			return nil, fmt.Errorf("session egress to %s exceeds 16 headers", destination.GetOrigin())
		}
		rule.Effects.ReplaceHeaders = append(rule.Effects.ReplaceHeaders, header)
	}
	if len(result.Rules) > 256 {
		return nil, fmt.Errorf("egress policy exceeds 256 rules")
	}
	return result, nil
}

// exactHTTPSRule returns the policy's rule for exactly hostname and port over
// HTTPS, appending one when there is none. A plain HTTP rule on the same
// hostname and port is a conflict.
func exactHTTPSRule(policy *ateapipb.EgressPolicy, hostname string, port int32) (*ateapipb.HTTPSRule, error) {
	exact := func(hostnames []string, ports *ateapipb.Ports) bool {
		return slices.Equal(hostnames, []string{hostname}) && slices.Equal(ports.GetNumbers(), []int32{port})
	}
	for _, rule := range policy.Rules {
		if https := rule.GetHttps(); https != nil && exact(https.GetHostnames(), https.GetPorts()) {
			return https, nil
		}
		if http := rule.GetHttp(); http != nil && exact(http.GetHostnames(), http.GetPorts()) {
			return nil, fmt.Errorf("session egress to %s:%d over HTTPS conflicts with its actor's HTTP rule", hostname, port)
		}
	}
	rule := &ateapipb.HTTPSRule{Hostnames: []string{hostname}, Ports: &ateapipb.Ports{Numbers: []int32{port}}}
	policy.Rules = append(policy.Rules, &ateapipb.EgressRule{Https: rule})
	return rule, nil
}

// sessionCredentialHeader compiles a session destination's authentication into
// the header the gateway replaces, from exactly one source.
func sessionCredentialHeader(authentication *apiv1.SessionEgressAuthentication) (*ateapipb.CredentialHeader, error) {
	header := strings.ToLower(authentication.GetHeader())
	if !egress.ValidCredentialHeader(header) || !httpguts.ValidHeaderFieldValue(authentication.GetPrefix()) {
		return nil, fmt.Errorf("invalid credential header %q", authentication.GetHeader())
	}
	result := &ateapipb.CredentialHeader{Header: header, Prefix: authentication.GetPrefix()}
	switch source := authentication.GetSource().(type) {
	case *apiv1.SessionEgressAuthentication_CredentialUri:
		result.CredentialUri = source.CredentialUri
	case *apiv1.SessionEgressAuthentication_ActorJwt:
		if len(source.ActorJwt.GetAudiences()) == 0 {
			return nil, fmt.Errorf("an actor JWT needs an audience")
		}
		result.ActorJwt = &ateapipb.ActorJWTSource{Audiences: slices.Clone(source.ActorJwt.GetAudiences()), ExpirationSeconds: sessionActorJWTLifetime}
	default:
		return nil, fmt.Errorf("header %s names no credential source", header)
	}
	return result, nil
}
