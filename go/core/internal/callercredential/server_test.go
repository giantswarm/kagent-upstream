package callercredential

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	now := time.Now()
	server.now = func() time.Time { return now }

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
	require.Equal(t, codes.FailedPrecondition, status.Code(err), "a turn without a bearer acts as no one")
	end()

	end = turns.Begin(testActor, "0123", callerSession{bearer: "alice"})
	broker.err = fmt.Errorf("%w: invalid_target no grant for github at http://muster.internal", ErrNoGrant)
	_, err = fetch(t, server, bearerURI)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	require.Equal(t, "the caller has no github grant at the token broker; sign in to github and retry", status.Convert(err).Message(),
		"the agent reads the remedy, not the broker's internals")
	end()

	defer turns.Begin(testActor, "0123", callerSession{bearer: "alice"})()
	broker.err = fmt.Errorf("token exchange: Post \"http://muster.agent-platform.svc:8090/oauth/token\": connection refused")
	_, err = fetch(t, server, bearerURI)
	require.Equal(t, codes.Unavailable, status.Code(err))
	require.Equal(t, "the token broker is unavailable", status.Convert(err).Message())
}

func TestFetchSecretAnswersARefusalAgainUntilItLapses(t *testing.T) {
	turns := NewTurns()
	broker := &fakeBroker{err: fmt.Errorf("%w: invalid_target", ErrNoGrant)}
	server := NewServer(turns, forwardingAuthenticator{}, broker)
	now := time.Now()
	server.now = func() time.Time { return now }
	defer turns.Begin(testActor, "0123", callerSession{bearer: "alice"})()

	for range 3 {
		_, err := fetch(t, server, bearerURI)
		require.Equal(t, codes.PermissionDenied, status.Code(err))
	}
	require.Len(t, broker.calls, 1, "a client's retries do not each ask the broker")

	broker.err = nil
	now = now.Add(refusalLifetime)
	response, err := fetch(t, server, bearerURI)
	require.NoError(t, err, "a person who signed in mid-turn gets in once the refusal lapses")
	require.Equal(t, "gh-alice", string(response.GetOpaqueBytes()))
}

func TestFetchSecretTellsAnExpiredSignInWithoutAskingTheBroker(t *testing.T) {
	turns := NewTurns()
	broker := &fakeBroker{}
	server := NewServer(turns, forwardingAuthenticator{}, broker)
	now := time.Now()
	server.now = func() time.Time { return now }
	claims := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"sub":"alice","exp":%d}`, now.Add(-time.Minute).Unix()))
	defer turns.Begin(testActor, "0123", callerSession{bearer: "header." + claims + ".signature"})()

	_, err := fetch(t, server, bearerURI)
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Contains(t, status.Convert(err).Message(), "sign-in expired")
	require.Empty(t, broker.calls)
}

// blockingBroker holds every exchange until released.
type blockingBroker struct {
	calls   atomic.Int32
	release chan struct{}
}

func (b *blockingBroker) Exchange(_ context.Context, subject, _ string, now time.Time) (string, time.Time, error) {
	b.calls.Add(1)
	<-b.release
	return "gh-" + subject, now.Add(testTokenLife), nil
}

func TestFetchSecretSharesOneExchangeAndAnswersNoEndedTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		turns := NewTurns()
		broker := &blockingBroker{release: make(chan struct{})}
		server := NewServer(turns, forwardingAuthenticator{}, broker)
		end := turns.Begin(testActor, "0123", callerSession{bearer: "alice"})
		defer end()

		codesSeen := make(chan codes.Code, 4)
		var fetches sync.WaitGroup
		for range 4 {
			fetches.Go(func() {
				_, err := fetch(t, server, bearerURI)
				codesSeen <- status.Code(err)
			})
		}
		synctest.Wait()
		require.Equal(t, int32(1), broker.calls.Load(), "concurrent requests of a turn share its exchange")

		// A slow exchange holds neither the registry nor a new turn on the
		// actor: Begin returns while alice's exchange is still in flight.
		defer turns.Begin(testActor, "0123", callerSession{bearer: "bob"})()

		close(broker.release)
		fetches.Wait()
		close(codesSeen)
		for code := range codesSeen {
			require.Equal(t, codes.NotFound, code, "a token exchanged for a turn that ended is answered to no one")
		}
	})
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
