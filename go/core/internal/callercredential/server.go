package callercredential

import (
	"context"
	"encoding/base64"
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
)

const actorSPIFFEPrefix = "spiffe://substrate-actor.local/atespace/"

// tokenExpiryMargin is how long before its expiry an exchanged token is
// exchanged again, so a request never carries one that expires in flight.
const tokenExpiryMargin = 30 * time.Second

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
		return nil, status.Errorf(codes.NotFound, "no turn is running on actor %s: a caller credential acts only as the person whose turn runs", actor)
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
func (s *Server) token(ctx context.Context, turn *Turn, audience string) (string, error) {
	turn.mu.Lock()
	defer turn.mu.Unlock()
	if turn.ended {
		return "", status.Error(codes.NotFound, "the turn ended")
	}
	now := s.now()
	if cached, ok := turn.tokens[audience]; ok && now.Add(tokenExpiryMargin).Before(cached.expires) {
		return cached.value, nil
	}
	subject, err := s.subject(turn)
	if err != nil {
		return "", err
	}
	token, expires, err := s.broker.Exchange(ctx, subject, audience, now)
	switch {
	case errors.Is(err, ErrNoGrant):
		return "", status.Errorf(codes.PermissionDenied, "the caller has no %s grant at the token broker: %v", audience, err)
	case err != nil:
		return "", status.Errorf(codes.Unavailable, "exchange the caller's token for %s: %v", audience, err)
	}
	turn.tokens[audience] = exchangedToken{value: token, expires: expires}
	return token, nil
}

// subject reads the caller's bearer from the turn's session the way the
// gateway forwards it to the runtime.
func (s *Server) subject(turn *Turn) (string, error) {
	request, err := http.NewRequest(http.MethodPost, "http://caller-credential.invalid", nil)
	if err != nil {
		return "", status.Error(codes.Internal, err.Error())
	}
	principal := auth.Principal{Agent: auth.Agent{ID: turn.instanceID}}
	if err := s.authenticator.UpstreamAuth(request, turn.session, principal); err != nil {
		return "", status.Errorf(codes.Internal, "read the turn caller's credential: %v", err)
	}
	subject, ok := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !ok || strings.TrimSpace(subject) == "" {
		return "", status.Error(codes.NotFound, "the turn's caller sent no bearer token")
	}
	return strings.TrimSpace(subject), nil
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
