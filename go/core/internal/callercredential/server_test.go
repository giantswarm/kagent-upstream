package callercredential

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/credproviderpb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/kagent-dev/kagent/go/core/pkg/auth"
)

const (
	testActorID   = "spiffe://substrate-actor.local/atespace/team-a/actor/ai-0123"
	testActor     = "team-a/ai-0123"
	basicURI      = "ate-secret://kagent.dev/caller/github/basic/x-access-token"
	bearerURI     = "ate-secret://kagent.dev/caller/github/bearer"
	testTokenLife = time.Hour
)

type callerSession struct{ bearer string }

func (callerSession) Principal() auth.Principal { return auth.Principal{} }

type forwardingAuthenticator struct{ auth.AuthProvider }

func (forwardingAuthenticator) UpstreamAuth(request *http.Request, session auth.Session, _ auth.Principal) error {
	if bearer := session.(callerSession).bearer; bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	return nil
}

type fakeBroker struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (b *fakeBroker) Exchange(_ context.Context, subject, audience string, now time.Time) (string, time.Time, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, subject+"@"+audience)
	if b.err != nil {
		return "", time.Time{}, b.err
	}
	return "gh-" + subject, now.Add(testTokenLife), nil
}

func fetch(t *testing.T, server *Server, uri string) (*credproviderpb.FetchSecretResponse, error) {
	t.Helper()
	return server.FetchSecret(t.Context(), &credproviderpb.FetchSecretRequest{Uri: uri, ActorSpiffeId: testActorID})
}

func TestFetchSecretActsOnlyAsTheCallerOfTheRunningTurn(t *testing.T) {
	turns := NewTurns()
	broker := &fakeBroker{}
	server := NewServer(turns, forwardingAuthenticator{}, broker)

	_, err := fetch(t, server, basicURI)
	require.Equal(t, codes.NotFound, status.Code(err), "no turn, no credential")

	end := turns.Begin(testActor, "0123", callerSession{bearer: "alice"})
	response, err := fetch(t, server, basicURI)
	require.NoError(t, err)
	require.Equal(t, base64.StdEncoding.EncodeToString([]byte("x-access-token:gh-alice")), string(response.GetOpaqueBytes()))
	require.NotNil(t, response.GetMaxAge(), "the gateway must not fall back to its own cache lifetime")
	require.Zero(t, response.GetMaxAge().AsDuration(), "the gateway must not reuse the answer")
	response, err = fetch(t, server, bearerURI)
	require.NoError(t, err)
	require.Equal(t, "gh-alice", string(response.GetOpaqueBytes()))
	require.Equal(t, []string{"alice@github"}, broker.calls, "one exchange per turn and audience")

	end()
	_, err = fetch(t, server, bearerURI)
	require.Equal(t, codes.NotFound, status.Code(err), "the turn's end ends its credential")

	defer turns.Begin(testActor, "0123", callerSession{bearer: "bob"})()
	response, err = fetch(t, server, bearerURI)
	require.NoError(t, err)
	require.Equal(t, "gh-bob", string(response.GetOpaqueBytes()), "the next turn acts as its own caller")
}

func TestFetchSecretExchangesAgainBeforeTheTokenExpires(t *testing.T) {
	turns := NewTurns()
	broker := &fakeBroker{}
	server := NewServer(turns, forwardingAuthenticator{}, broker)
	now := time.Now()
	server.now = func() time.Time { return now }
	defer turns.Begin(testActor, "0123", callerSession{bearer: "alice"})()

	_, err := fetch(t, server, bearerURI)
	require.NoError(t, err)
	now = now.Add(testTokenLife - tokenExpiryMargin)
	_, err = fetch(t, server, bearerURI)
	require.NoError(t, err)
	require.Len(t, broker.calls, 2)
}

func TestFetchSecretRefusals(t *testing.T) {
	turns := NewTurns()
	broker := &fakeBroker{}
	server := NewServer(turns, forwardingAuthenticator{}, broker)

	for _, request := range []*credproviderpb.FetchSecretRequest{
		{Uri: "ate-secret://kubernetes.io/team-a/secret/key", ActorSpiffeId: testActorID},
		{Uri: bearerURI, ActorSpiffeId: "spiffe://cluster.local/ns/team-a/sa/default"},
		{Uri: bearerURI, ActorSpiffeId: "spiffe://substrate-actor.local/atespace/team-a/actor/ai-0123/extra"},
	} {
		_, err := server.FetchSecret(t.Context(), request)
		require.Equal(t, codes.InvalidArgument, status.Code(err), request.String())
	}

	end := turns.Begin(testActor, "0123", callerSession{})
	_, err := fetch(t, server, bearerURI)
	require.Equal(t, codes.NotFound, status.Code(err), "a turn without a bearer acts as no one")
	end()

	defer turns.Begin(testActor, "0123", callerSession{bearer: "alice"})()
	broker.err = fmt.Errorf("%w: invalid_target", ErrNoGrant)
	_, err = fetch(t, server, bearerURI)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	broker.err = fmt.Errorf("connection refused")
	_, err = fetch(t, server, bearerURI)
	require.Equal(t, codes.Unavailable, status.Code(err))
}

func TestTurnsKeepTheLatestTurnOfAnActor(t *testing.T) {
	turns := NewTurns()
	endFirst := turns.Begin(testActor, "0123", callerSession{bearer: "alice"})
	first, _ := turns.Current(testActor)
	endSecond := turns.Begin(testActor, "0123", callerSession{bearer: "bob"})
	require.True(t, first.ended, "a replaced turn holds no credential")

	endFirst()
	current, ok := turns.Current(testActor)
	require.True(t, ok, "ending a replaced turn leaves its successor")
	require.Equal(t, "bob", current.session.(callerSession).bearer)
	endSecond()
	_, ok = turns.Current(testActor)
	require.False(t, ok)
}
