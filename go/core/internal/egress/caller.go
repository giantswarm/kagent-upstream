package egress

import (
	"fmt"
	"regexp"
	"strings"
)

// CallerCredentialProvider is the credential URI authority the controller
// serves to the egress gateway: a credential under it is the token of the
// person whose turn runs on the actor, exchanged for an audience.
const CallerCredentialProvider = "kagent.dev"

const callerCredentialURIPrefix = "ate-secret://" + CallerCredentialProvider + "/caller/"

// CallerScheme is the HTTP authentication scheme a caller credential is
// rendered for.
type CallerScheme string

const (
	CallerSchemeBearer CallerScheme = "bearer"
	CallerSchemeBasic  CallerScheme = "basic"
)

// CallerCredential is what a caller credential URI names.
type CallerCredential struct {
	Audience string
	Scheme   CallerScheme
	// Username precedes the token in a Basic credential.
	Username string
}

var (
	callerAudiencePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	callerUsernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

// URI renders the credential as ate-secret://kagent.dev/caller/<audience>/bearer
// or ate-secret://kagent.dev/caller/<audience>/basic/<username>.
func (c CallerCredential) URI() string {
	uri := callerCredentialURIPrefix + c.Audience + "/" + string(c.Scheme)
	if c.Scheme == CallerSchemeBasic {
		uri += "/" + c.Username
	}
	return uri
}

// ParseCallerCredentialURI is the inverse of URI and refuses every other shape.
func ParseCallerCredentialURI(uri string) (CallerCredential, error) {
	rest, ok := strings.CutPrefix(uri, callerCredentialURIPrefix)
	if !ok {
		return CallerCredential{}, fmt.Errorf("credential URI %q is not a caller credential", uri)
	}
	parts := strings.Split(rest, "/")
	credential := CallerCredential{Audience: parts[0]}
	if len(credential.Audience) > 63 || !callerAudiencePattern.MatchString(credential.Audience) {
		return CallerCredential{}, fmt.Errorf("caller credential URI %q names an invalid audience", uri)
	}
	switch {
	case len(parts) == 2 && parts[1] == string(CallerSchemeBearer):
		credential.Scheme = CallerSchemeBearer
	case len(parts) == 3 && parts[1] == string(CallerSchemeBasic) && callerUsernamePattern.MatchString(parts[2]):
		credential.Scheme, credential.Username = CallerSchemeBasic, parts[2]
	default:
		return CallerCredential{}, fmt.Errorf("caller credential URI %q must end in /bearer or /basic/<username>", uri)
	}
	return credential, nil
}
