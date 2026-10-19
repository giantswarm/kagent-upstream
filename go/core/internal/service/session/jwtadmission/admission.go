// Package jwtadmission admits a Session volume source with a short-lived JWT
// grant: signed by an issuer the installation trusts, verified against the
// issuer's published JWKS, naming the caller, the namespace of the Agent the
// Session may run on, the volume and the mounts of it the Session may have,
// and carrying what the Session is admitted with: the repositories changed
// since the volume's last sync, and the origins its actor may reach beyond its
// Agent's egress with the credential the egress gateway sets on each. The
// claims that carry them are named by the installation.
package jwtadmission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/net/http/httpguts"
	"k8s.io/apimachinery/pkg/util/validation"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/internal/service/serviceerrors"
	"github.com/kagent-dev/kagent/go/core/internal/service/session"
)

// leeway absorbs the clock skew between the issuer and the controller on exp
// and nbf.
const leeway = 30 * time.Second

// signingMethods are the algorithms a grant may be signed with.
var signingMethods = []string{
	jwt.SigningMethodES256.Alg(), jwt.SigningMethodES384.Alg(),
	jwt.SigningMethodRS256.Alg(), jwt.SigningMethodPS256.Alg(),
}

// Config names the grant issuer an installation trusts and the claims its
// grants carry.
type Config struct {
	// JWKSURL is where the issuer publishes its verification keys: an https
	// URL, since whoever can rewrite the keys in transit can sign grants.
	JWKSURL string
	// AllowHTTP admits a plain http JWKSURL, for an in-cluster issuer that
	// serves no TLS.
	AllowHTTP bool
	// Issuer is the grant's required iss.
	Issuer string
	// Audience is the grant's required aud: this installation's kagent.
	Audience string
	// Claims names the claims that carry what a grant admits. An empty name
	// takes its default from DefaultClaims.
	Claims Claims
	// CredentialAuthority is the credential provider a grant's egress
	// credentials are resolved by, ate-secret://<provider-class>/<provider-name>.
	// Empty, a grant that names a credential is refused.
	CredentialAuthority string
}

// Claims names the claims of a grant that carry what it admits.
type Claims struct {
	// AgentNamespace is the claim naming the namespace of the Agents a
	// session may run on: a string.
	AgentNamespace string
	// Volume is the claim naming the volume: {"driver": ..., "handle": ...}.
	Volume string
	// Mounts is the claim listing the directories of the volume a session may
	// mount: [{"sub_path": ..., "read_only": ...}].
	Mounts string
	// Changed is the optional claim listing paths, relative to the session's
	// working directory, of the repositories whose origin moved since the
	// volume was last synced.
	Changed string
	// Egress is the optional claim listing the origins the session's actor may
	// reach beyond its Agent's egress: [{"origin": ..., "authentication":
	// {"header": ..., "prefix": ..., "credential": ...}}], or "actor_jwt":
	// {"audiences": [...]} in place of "credential".
	Egress string
}

// DefaultClaims are the claim names of a grant whose issuer names none.
var DefaultClaims = Claims{AgentNamespace: "agent_namespace", Volume: "volume", Mounts: "mounts", Changed: "changed", Egress: "egress"}

// withDefaults fills every empty name from DefaultClaims.
func (c Claims) withDefaults() Claims {
	if c.AgentNamespace == "" {
		c.AgentNamespace = DefaultClaims.AgentNamespace
	}
	if c.Volume == "" {
		c.Volume = DefaultClaims.Volume
	}
	if c.Mounts == "" {
		c.Mounts = DefaultClaims.Mounts
	}
	if c.Changed == "" {
		c.Changed = DefaultClaims.Changed
	}
	if c.Egress == "" {
		c.Egress = DefaultClaims.Egress
	}
	return c
}

// validate refuses a name used twice or one of the registered claims the
// admission verifies itself.
func (c Claims) validate() error {
	taken := map[string]bool{"iss": true, "sub": true, "aud": true, "exp": true, "nbf": true, "iat": true, "jti": true}
	for _, name := range []string{c.AgentNamespace, c.Volume, c.Mounts, c.Changed, c.Egress} {
		if taken[name] {
			return fmt.Errorf("claim name %q is a registered claim or names two claims", name)
		}
		taken[name] = true
	}
	return nil
}

// Admission verifies workspace grants. It is safe for concurrent use.
type Admission struct {
	issuer, audience    string
	claims              Claims
	credentialAuthority string
	keys                *keySet
}

// ErrPlainHTTP is New's refusal of a plain http JWKS URL the configuration
// does not allow.
var ErrPlainHTTP = errors.New("the keys could be replaced in transit; use https, or allow http explicitly")

// Refused is the admission a configuration New refused leaves the controller
// with: it refuses every grant with Cause, so a misconfigured admission
// refuses Session volume sources and stops nothing else.
type Refused struct {
	Cause error
}

// Admit refuses the grant: the installation's admission is misconfigured.
func (r Refused) Admit(context.Context, string, *apiv1.ResourceReference, *apiv1.SessionVolumeSource, string) (session.Admitted, error) {
	return session.Admitted{}, serviceerrors.NewFailedPrecondition("this installation's Session admission is misconfigured and admits no volume source", r.Cause)
}

// New returns the admission for config. It fetches no key until the first
// grant arrives, so an unreachable issuer never stops the controller.
func New(config Config, client *http.Client) (*Admission, error) {
	location, err := url.Parse(config.JWKSURL)
	if err != nil || (location.Scheme != "https" && location.Scheme != "http") || location.Host == "" {
		return nil, fmt.Errorf("JWKS URL %q is not an absolute https URL", config.JWKSURL)
	}
	if location.Scheme == "http" && !config.AllowHTTP {
		return nil, fmt.Errorf("JWKS URL %q is plain http: %w", config.JWKSURL, ErrPlainHTTP)
	}
	if config.Issuer == "" || config.Audience == "" {
		return nil, errors.New("a JWT admission needs an issuer and an audience")
	}
	claims := config.Claims.withDefaults()
	if err := claims.validate(); err != nil {
		return nil, err
	}
	if config.CredentialAuthority != "" && !validCredentialAuthority(config.CredentialAuthority) {
		return nil, fmt.Errorf("credential authority %q is not ate-secret://<provider-class>/<provider-name> of a provider other than Kubernetes Secrets", config.CredentialAuthority)
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Admission{issuer: config.Issuer, audience: config.Audience, claims: claims, credentialAuthority: config.CredentialAuthority, keys: newKeySet(location.String(), client)}, nil
}

// validCredentialAuthority reports whether authority names one credential
// provider by class and name. Substrate's Kubernetes Secret provider is no
// authority: through it a grant would name a Secret of the namespace.
func validCredentialAuthority(authority string) bool {
	location, err := url.Parse(authority)
	if err != nil || location.Scheme != "ate-secret" || location.User != nil || location.RawQuery != "" || location.Fragment != "" || location.Port() != "" {
		return false
	}
	name, ok := strings.CutPrefix(location.Path, "/")
	return ok && location.Host != "" && location.Host != "k8s.io" && len(validation.IsDNS1123Subdomain(location.Host)) == 0 && len(validation.IsDNS1123Label(name)) == 0
}

// volume is the grant's volume claim: a CSI volume by its driver and handle.
type volume struct {
	Driver string `json:"driver"`
	Handle string `json:"handle"`
}

// mount is a directory of the volume the grant allows, spelled as a volume
// source's sub-path ("sessions/${SESSION_ID}"). A read-write allowance also
// admits the directory read-only.
type mount struct {
	SubPath  string `json:"sub_path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// payload is a grant's payload: the registered claims, and every claim by
// name for the ones the installation names.
type payload struct {
	jwt.RegisteredClaims
	named map[string]json.RawMessage
}

func (p *payload) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &p.RegisteredClaims); err != nil {
		return err
	}
	return json.Unmarshal(data, &p.named)
}

// egressGrant is an origin the grant lets the session's actor reach, in the
// vocabulary of an EgressPolicy destination, with the header the egress
// gateway sets on it from a credential of the installation's credential
// authority or from the actor's own JWT.
type egressGrant struct {
	Origin         string `json:"origin"`
	Authentication *struct {
		Header     string `json:"header"`
		Prefix     string `json:"prefix,omitempty"`
		Credential string `json:"credential,omitempty"`
		ActorJWT   *struct {
			Audiences []string `json:"audiences"`
		} `json:"actor_jwt,omitempty"`
	} `json:"authentication,omitempty"`
}

// allowance is what a verified payload admits, read from the configured claims.
type allowance struct {
	agentNamespace string
	volume         volume
	mounts         []mount
	changed        []string
	egress         []egressGrant
}

// read decodes the configured claims of p. A missing Agent namespace, volume
// or mounts claim reads as none, which admits no source.
func (c Claims) read(p *payload) (allowance, error) {
	var result allowance
	for _, claim := range []struct {
		name string
		into any
	}{
		{c.AgentNamespace, &result.agentNamespace}, {c.Volume, &result.volume}, {c.Mounts, &result.mounts}, {c.Changed, &result.changed}, {c.Egress, &result.egress},
	} {
		raw, ok := p.named[claim.name]
		if !ok {
			continue
		}
		if err := json.Unmarshal(raw, claim.into); err != nil {
			return allowance{}, serviceerrors.NewPermissionDenied(fmt.Sprintf("admission grant is malformed: claim %q", claim.name), err)
		}
	}
	return result, nil
}

// Admit verifies token and admits caller to a Session of agent on source with
// the grant's changed repositories and egress. Every refusal is PermissionDenied with its
// reason, except an issuer whose keys cannot be fetched, which is Unavailable:
// the grant may well be valid.
func (a *Admission) Admit(ctx context.Context, caller string, agent *apiv1.ResourceReference, source *apiv1.SessionVolumeSource, token string) (session.Admitted, error) {
	if token == "" {
		return session.Admitted{}, serviceerrors.NewPermissionDenied("no admission grant", nil)
	}
	if source == nil {
		return session.Admitted{}, serviceerrors.NewInvalidArgument("an admission grant admits a volume_source and the request names none", nil)
	}
	verified := &payload{}
	_, err := jwt.ParseWithClaims(token, verified, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, errUnknownKey
		}
		return a.keys.key(ctx, kid)
	},
		jwt.WithValidMethods(signingMethods),
		jwt.WithIssuer(a.issuer), jwt.WithAudience(a.audience),
		jwt.WithExpirationRequired(), jwt.WithLeeway(leeway),
	)
	if err != nil {
		return session.Admitted{}, refusal(err)
	}
	if verified.Subject != caller {
		return session.Admitted{}, serviceerrors.NewPermissionDenied("admission grant names another person", nil)
	}
	granted, err := a.claims.read(verified)
	if err != nil {
		return session.Admitted{}, err
	}
	if err := allows(granted, agent, source); err != nil {
		return session.Admitted{}, err
	}
	return a.admitted(granted)
}

// refusal maps a failed verification to its named reason, most specific
// first. Key material and the token never reach the message.
func refusal(err error) error {
	for _, reason := range []struct {
		cause   error
		message string
	}{
		{errUnknownKey, "admission grant is signed by an unknown key"},
		{jwt.ErrTokenSignatureInvalid, "admission grant signature is invalid"},
		{jwt.ErrTokenInvalidIssuer, "admission grant is from an unknown issuer"},
		{jwt.ErrTokenInvalidAudience, "admission grant is for another audience"},
		{jwt.ErrTokenExpired, "admission grant expired"},
		{jwt.ErrTokenNotValidYet, "admission grant is not valid yet"},
	} {
		if errors.Is(err, reason.cause) {
			return serviceerrors.NewPermissionDenied(reason.message, err)
		}
	}
	if errors.Is(err, errKeysUnavailable) {
		return serviceerrors.NewUnavailable("admission grant keys are unavailable", err)
	}
	return serviceerrors.NewPermissionDenied("admission grant is malformed", err)
}

// allows refuses a Session of an Agent outside the grant's namespace, and a
// source on another volume than the grant's or with a mount the grant does not
// allow. A grant that names no namespace is refused too: it would admit an
// Agent in any.
func allows(granted allowance, agent *apiv1.ResourceReference, source *apiv1.SessionVolumeSource) error {
	if granted.agentNamespace == "" || granted.agentNamespace != agent.GetNamespace() {
		return serviceerrors.NewPermissionDenied("admission grant is for another namespace", nil)
	}
	if granted.volume.Driver != source.GetVolume().GetCsiDriver() || granted.volume.Handle != source.GetVolume().GetVolumeHandle() {
		return serviceerrors.NewPermissionDenied("admission grant names another volume", nil)
	}
	for _, requested := range source.GetMounts() {
		if !mountAllowed(granted.mounts, requested) {
			access := "read-only"
			if !requested.GetReadOnly() {
				access = "read-write"
			}
			return serviceerrors.NewPermissionDenied(fmt.Sprintf("admission grant does not allow the %s mount of %q", access, requested.GetSubPath()), nil)
		}
	}
	return nil
}

func mountAllowed(allowed []mount, requested *apiv1.SessionVolumeMount) bool {
	for _, allowance := range allowed {
		if allowance.SubPath == requested.GetSubPath() && (requested.GetReadOnly() || !allowance.ReadOnly) {
			return true
		}
	}
	return false
}

// admitted is what the grant admits the Session with, refused as malformed
// when a changed path or an egress destination is not one a Session can carry.
func (a *Admission) admitted(granted allowance) (session.Admitted, error) {
	for _, changed := range granted.changed {
		if changed == "" || changed != path.Clean(changed) || path.IsAbs(changed) || changed == ".." || strings.HasPrefix(changed, "../") {
			return session.Admitted{}, serviceerrors.NewPermissionDenied("admission grant is malformed: a changed repository is not a clean relative path", nil)
		}
	}
	egress, err := a.sessionEgress(granted.egress)
	if err != nil {
		return session.Admitted{}, err
	}
	return session.Admitted{Changed: granted.changed, Egress: egress}, nil
}

// maxEgress bounds the destinations one grant may add to a session's actor.
const maxEgress = 16

// sessionEgress turns the grant's egress into the session's: each origin an
// exact https origin with its port explicit, each header set once per origin,
// a credential resolved under the installation's credential authority. A grant
// never names the provider a credential comes from.
func (a *Admission) sessionEgress(grants []egressGrant) ([]*apiv1.SessionEgressDestination, error) {
	malformed := func(reason string) error {
		return serviceerrors.NewPermissionDenied("admission grant is malformed: "+reason, nil)
	}
	if len(grants) > maxEgress {
		return nil, malformed(fmt.Sprintf("more than %d egress destinations", maxEgress))
	}
	var result []*apiv1.SessionEgressDestination
	seen := map[string]bool{}
	for _, grant := range grants {
		origin, err := egress.ParseOrigin(grant.Origin)
		if err != nil || origin.Scheme != "https" || strings.HasPrefix(origin.Hostname(), "*.") {
			return nil, malformed(fmt.Sprintf("egress origin %q is not an exact https origin", grant.Origin))
		}
		destination := &apiv1.SessionEgressDestination{Origin: origin.String()}
		header := ""
		if authentication := grant.Authentication; authentication != nil {
			header = strings.ToLower(authentication.Header)
			if !egress.ValidCredentialHeader(header) || !httpguts.ValidHeaderFieldValue(authentication.Prefix) {
				return nil, malformed(fmt.Sprintf("egress to %s sets an invalid header", destination.Origin))
			}
			destination.Authentication = &apiv1.SessionEgressAuthentication{Header: header, Prefix: authentication.Prefix}
			switch {
			case authentication.Credential != "" && authentication.ActorJWT == nil:
				if !validCredentialName(authentication.Credential) {
					return nil, malformed(fmt.Sprintf("egress to %s names an invalid credential", destination.Origin))
				}
				if a.credentialAuthority == "" {
					return nil, serviceerrors.NewFailedPrecondition("this installation resolves no Session egress credential", nil)
				}
				destination.Authentication.Source = &apiv1.SessionEgressAuthentication_CredentialUri{CredentialUri: a.credentialAuthority + "/" + authentication.Credential}
			case authentication.Credential == "" && authentication.ActorJWT != nil:
				audiences := authentication.ActorJWT.Audiences
				if len(audiences) == 0 || len(audiences) > 16 || slices.ContainsFunc(audiences, func(audience string) bool { return audience == "" || len(audience) > 512 }) {
					return nil, malformed(fmt.Sprintf("egress to %s binds an actor JWT to no or invalid audiences", destination.Origin))
				}
				destination.Authentication.Source = &apiv1.SessionEgressAuthentication_ActorJwt{ActorJwt: &apiv1.SessionActorJWT{Audiences: slices.Clone(audiences)}}
			default:
				return nil, malformed(fmt.Sprintf("egress to %s needs exactly one of credential and actor_jwt", destination.Origin))
			}
		}
		key := destination.Origin + "\x00" + header
		if seen[key] {
			return nil, malformed(fmt.Sprintf("egress to %s is granted twice", destination.Origin))
		}
		seen[key] = true
		result = append(result, destination)
	}
	return result, nil
}

// validCredentialName reports whether name is a clean relative path of the
// credential provider's own names: segments of letters, digits, '.', '_' and
// '-', none of them "." or "..".
func validCredentialName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for segment := range strings.SplitSeq(name, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsFunc(segment, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != '-'
		}) {
			return false
		}
	}
	return true
}
