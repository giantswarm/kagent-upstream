package callercredential

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/credproviderpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/pkg/auth"
	"github.com/kagent-dev/kagent/go/pkg/logging"
)

const actorSPIFFEPrefix = "spiffe://substrate-actor.local/atespace/"

// tokenExpiryMargin is how long before its expiry an exchanged token is
// exchanged again, so a request never carries one that expires in flight.
const tokenExpiryMargin = 30 * time.Second

// refusalLifetime is how long a turn answers a caller refusal again without
// asking the broker: short, so a person who signs in mid-turn is not kept out.
const refusalLifetime = 30 * time.Second

// The messages of these refusals reach the agent through the egress gateway
// (git prints them as remote: lines), so they name the remedy and nothing of
// the platform's internals; the details are logged.
var (
	errNoTurn        = status.Error(codes.NotFound, "no turn is running on this agent; a caller credential is only set during a person's turn")
	errTurnEnded     = status.Error(codes.NotFound, "the turn ended; a caller credential is only set during a person's turn")
	errNoBearer      = status.Error(codes.FailedPrecondition, "the platform forwarded no sign-in for this turn's caller")
	errSignInExpired = status.Error(codes.FailedPrecondition, "the caller's sign-in expired during the turn; send the message again to start a new turn")
	errBrokerDown    = status.Error(codes.Unavailable, "the token broker is unavailable")
	errLookupFailed  = status.Error(codes.Internal, "caller credential lookup failed")
)

func errNoGrantFor(audience string) error {
	return status.Errorf(codes.PermissionDenied, "the caller has no %s grant at the token broker; sign in to %s and retry", audience, audience)
}

type exchanger interface {
	Exchange(ctx context.Context, subject, audience string, now time.Time) (string, time.Time, error)
}

// Server answers the egress gateway's FetchSecret for caller credentials.
type Server struct {
	credproviderpb.UnimplementedCredentialProviderServer

	turns         *Turns
	authenticator auth.AuthProvider
	broker        exchanger
	now           func() time.Time
}

// NewServer returns the provider over turns, reading each turn's caller token
// through authenticator and exchanging it at broker.
func NewServer(turns *Turns, authenticator auth.AuthProvider, broker exchanger) *Server {
	return &Server{turns: turns, authenticator: authenticator, broker: broker, now: time.Now}
}

// FetchSecret answers with the credential of the person whose turn runs on the
// actor, and tells the gateway not to reuse it: the next request may belong to
// another turn, or to none.
func (s *Server) FetchSecret(ctx context.Context, request *credproviderpb.FetchSecretRequest) (*credproviderpb.FetchSecretResponse, error) {
	credential, err := egress.ParseCallerCredentialURI(request.GetUri())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	actor, err := actorFromSPIFFEID(request.GetActorSpiffeId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	turn, ok := s.turns.Current(actor)
	if !ok {
		return nil, errNoTurn
	}
	token, err := s.token(ctx, turn, credential.Audience)
	if err != nil {
		return nil, err
	}
	value := token
	if credential.Scheme == egress.CallerSchemeBasic {
		value = base64.StdEncoding.EncodeToString([]byte(credential.Username + ":" + token))
	}
	return &credproviderpb.FetchSecretResponse{OpaqueBytes: []byte(value), MaxAge: durationpb.New(0)}, nil
}

// token returns the turn caller's token at audience, exchanging the caller's
// own token once per turn and again shortly before the result expires.
// Concurrent requests of the turn share one exchange, which runs detached from
// the request that started it so its cancellation does not fail the others.
func (s *Server) token(ctx context.Context, turn *Turn, audience string) (string, error) {
	if token, ok, err := turn.cached(audience, s.now()); ok {
		return token, err
	}
	token, err, _ := turn.exchanges.Do(audience, func() (any, error) {
		now := s.now()
		if token, ok, err := turn.cached(audience, now); ok {
			return token, err
		}
		token, expires, err := s.exchange(context.WithoutCancel(ctx), turn, audience, now)
		switch status.Code(err) {
		case codes.OK:
			if !turn.keep(audience, exchangedToken{value: token, expires: expires}, nil) {
				return "", errTurnEnded
			}
		case codes.PermissionDenied, codes.FailedPrecondition:
			turn.keep(audience, exchangedToken{}, &refusal{err: err, until: now.Add(refusalLifetime)})
		}
		return token, err
	})
	if err != nil {
		return "", err
	}
	return token.(string), nil
}

func (s *Server) exchange(ctx context.Context, turn *Turn, audience string, now time.Time) (string, time.Time, error) {
	log := logging.FromContext(ctx).With("instance_id", turn.instanceID, "audience", audience)
	subject, err := s.subject(turn)
	if err != nil {
		if !errors.Is(err, errNoBearer) {
			log.ErrorContext(ctx, "failed to read the turn caller's credential", "error", err)
			return "", time.Time{}, errLookupFailed
		}
		return "", time.Time{}, err
	}
	if expiry, ok := tokenExpiry(subject); ok && !now.Before(expiry) {
		log.InfoContext(ctx, "turn caller's sign-in expired before the exchange", "expired_at", expiry)
		return "", time.Time{}, errSignInExpired
	}
	token, expires, err := s.broker.Exchange(ctx, subject, audience, now)
	switch {
	case errors.Is(err, ErrNoGrant):
		log.InfoContext(ctx, "token broker refused the turn's caller", "error", err)
		return "", time.Time{}, errNoGrantFor(audience)
	case err != nil:
		log.ErrorContext(ctx, "failed to exchange the turn caller's token", "error", err)
		return "", time.Time{}, errBrokerDown
	}
	return token, expires, nil
}

// subject reads the caller's bearer from the turn's session the way the
// gateway forwards it to the runtime.
func (s *Server) subject(turn *Turn) (string, error) {
	request, err := http.NewRequest(http.MethodPost, "http://caller-credential.invalid", nil)
	if err != nil {
		return "", err
	}
	principal := auth.Principal{Agent: auth.Agent{ID: turn.instanceID}}
	if err := s.authenticator.UpstreamAuth(request, turn.session, principal); err != nil {
		return "", err
	}
	subject, ok := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(subject) == "" {
		return "", errNoBearer
	}
	return strings.TrimSpace(subject), nil
}

// tokenExpiry reads the exp claim of a JWT without verifying it: it only tells
// an expired sign-in from a missing grant before the broker is asked, and the
// broker still validates the token. An opaque token has no expiry here.
func tokenExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp json.Number `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, false
	}
	seconds, err := claims.Exp.Int64()
	if err != nil || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(seconds, 0), true
}

// actorFromSPIFFEID maps spiffe://substrate-actor.local/atespace/<a>/actor/<n>
// to the "<a>/<n>" the A2A gateway dispatches to.
func actorFromSPIFFEID(id string) (string, error) {
	rest, ok := strings.CutPrefix(id, actorSPIFFEPrefix)
	parts := strings.Split(rest, "/")
	if !ok || len(parts) != 3 || parts[1] != "actor" || len(validation.IsDNS1123Label(parts[0])) != 0 || len(validation.IsDNS1123Label(parts[2])) != 0 {
		return "", fmt.Errorf("actor SPIFFE ID %q is not an actor identity", id)
	}
	return parts[0] + "/" + parts[2], nil
}
