package jwks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kagent-dev/kagent/go/core/test/grant"
	"github.com/stretchr/testify/require"
)

// issuer serves the JWKS of its signers and counts the fetches.
type issuer struct {
	mu      sync.Mutex
	signers []*grant.Signer
	down    bool
	fetches int
}

func (i *issuer) publish(signers ...*grant.Signer) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.signers, i.down = signers, false
}

func (i *issuer) takeDown() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.down = true
}

func (i *issuer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.fetches++
	if i.down {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	var keys grant.JWKS
	for _, signer := range i.signers {
		keys.Keys = append(keys.Keys, signer.JWKS().Keys...)
	}
	_ = json.NewEncoder(w).Encode(keys)
}

// clock is the key set's time, moved by the test.
type clock struct{ now time.Time }

func (c *clock) advance(by time.Duration) { c.now = c.now.Add(by) }

func newTestKeySet(t *testing.T, published ...*grant.Signer) (*KeySet, *issuer, *clock) {
	t.Helper()
	keys := &issuer{}
	keys.publish(published...)
	server := httptest.NewTLSServer(keys)
	t.Cleanup(server.Close)
	set := NewKeySet(server.URL+grant.JWKSPath, server.Client())
	at := &clock{now: time.Now()}
	set.now = func() time.Time { return at.now }
	return set, keys, at
}

func newSigner(t *testing.T) *grant.Signer {
	t.Helper()
	signer, err := grant.NewSigner("https://issuer.example.test", "kagent")
	require.NoError(t, err)
	return signer
}

// A rotated JWKS is read on the first token with the new key id: no restart.
// The withdrawn key verifies nothing after.
func TestKeySetReadsARotatedKey(t *testing.T) {
	before := newSigner(t)
	set, keys, at := newTestKeySet(t, before)
	_, err := set.Key(t.Context(), before.KeyID())
	require.NoError(t, err)

	after := newSigner(t)
	keys.publish(after)
	at.advance(RefreshInterval)
	_, err = set.Key(t.Context(), after.KeyID())
	require.NoError(t, err, "the rotated key verifies")
	require.Equal(t, 2, keys.fetches)

	_, err = set.Key(t.Context(), before.KeyID())
	require.ErrorIs(t, err, ErrUnknownKey)
}

// Unknown key ids refetch the JWKS at most once per RefreshInterval.
func TestKeySetUnknownKeysDoNotHammerTheIssuer(t *testing.T) {
	signer := newSigner(t)
	set, keys, _ := newTestKeySet(t, signer)
	for range 5 {
		_, err := set.Key(t.Context(), newSigner(t).KeyID())
		require.ErrorIs(t, err, ErrUnknownKey)
	}
	require.Equal(t, 1, keys.fetches)
}

// Keys fetched within MaxAge verify while the issuer is down; older ones do
// not, so a key the issuer withdrew stops verifying.
func TestKeySetTrustsFetchedKeysForMaxAge(t *testing.T) {
	signer := newSigner(t)
	set, keys, at := newTestKeySet(t, signer)
	_, err := set.Key(t.Context(), signer.KeyID())
	require.NoError(t, err)

	keys.takeDown()
	_, err = set.Key(t.Context(), signer.KeyID())
	require.NoError(t, err)

	at.advance(MaxAge)
	_, err = set.Key(t.Context(), signer.KeyID())
	require.ErrorIs(t, err, ErrKeysUnavailable)
}
