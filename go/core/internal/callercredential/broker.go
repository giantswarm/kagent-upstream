package callercredential

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	tokenExchangeGrantType = "urn:ietf:params:oauth:grant-type:token-exchange"
	accessTokenType        = "urn:ietf:params:oauth:token-type:access_token"
	// IDTokenType is the subject token type of an OIDC ID token, what the
	// platform's edge forwards as the caller's bearer.
	IDTokenType = "urn:ietf:params:oauth:token-type:id_token"
)

// Broker exchanges a caller's token for that person's token at an audience
// (RFC 8693) as a confidential client authenticated with HTTP Basic.
type Broker struct {
	TokenURL         string
	ClientID         string
	ClientSecretFile string
	SubjectTokenType string
	HTTPClient       *http.Client
}

// ErrNoGrant is a broker refusal tied to the caller: no grant for the
// audience (invalid_target), or a subject token it does not accept
// (invalid_grant). Signing in again at the broker is the way out, not a retry.
var ErrNoGrant = errors.New("the token broker refused the caller")

// defaultTokenLifetime is how long a token the broker issued without
// expires_in is used before it is exchanged again.
const defaultTokenLifetime = time.Minute

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Exchange trades subject for the caller's token at audience and returns it
// with its expiry; a response without expires_in lasts defaultTokenLifetime.
func (b *Broker) Exchange(ctx context.Context, subject, audience string, now time.Time) (string, time.Time, error) {
	secret, err := os.ReadFile(b.ClientSecretFile)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read token broker client secret: %w", err)
	}
	subjectType := b.SubjectTokenType
	if subjectType == "" {
		subjectType = IDTokenType
	}
	form := url.Values{
		"grant_type":           {tokenExchangeGrantType},
		"subject_token":        {subject},
		"subject_token_type":   {subjectType},
		"audience":             {audience},
		"requested_token_type": {accessTokenType},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, b.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build token exchange request: %w", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	// RFC 6749 section 2.3.1: both halves are form-encoded before Basic.
	request.SetBasicAuth(url.QueryEscape(b.ClientID), url.QueryEscape(strings.TrimRight(string(secret), "\r\n")))
	client := b.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token exchange: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read token exchange response: %w", err)
	}
	var token tokenResponse
	_ = json.Unmarshal(body, &token)
	if response.StatusCode != http.StatusOK {
		switch token.Error {
		case "invalid_target", "invalid_grant":
			return "", time.Time{}, fmt.Errorf("%w: %s %s", ErrNoGrant, token.Error, token.ErrorDescription)
		}
		return "", time.Time{}, fmt.Errorf("token exchange answered %d %s", response.StatusCode, token.Error)
	}
	if token.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("token exchange answered no access_token")
	}
	lifetime := time.Duration(token.ExpiresIn) * time.Second
	if token.ExpiresIn <= 0 {
		lifetime = defaultTokenLifetime
	}
	return token.AccessToken, now.Add(lifetime), nil
}
