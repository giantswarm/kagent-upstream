package driver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func callRoute(t *testing.T, forwarder *CredentialForwarder, method, url string, body io.Reader) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), method, url, body)
	require.NoError(t, err)
	for header, value := range forwarder.Headers() {
		request.Header.Set(header, value)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestCallerRouteActsOnlyDuringATurn(t *testing.T) {
	upstream, requests := newRecordingUpstream(t)
	forwarder, err := NewCredentialForwarder(nil, map[string]CallerRoute{
		"github.com": {URL: upstream.URL + "/git/github.com/"},
	}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	route := forwarder.RouteURL("github.com")
	require.True(t, strings.HasPrefix(route, forwarder.BaseURL()), "route %q outside the forwarder's origin %q", route, forwarder.BaseURL())

	response := callRoute(t, forwarder, http.MethodGet, route+"owner/repo/info/refs?service=git-upload-pack", nil)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.Empty(t, requests(), "a route request without a turn reached the upstream")

	forwarder.Bind("Bearer person-token")
	response = callRoute(t, forwarder, http.MethodGet, route+"owner/repo/info/refs?service=git-upload-pack", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	forwarder.Clear()
	response = callRoute(t, forwarder, http.MethodGet, route+"owner/repo/info/refs?service=git-upload-pack", nil)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)

	seen := requests()
	require.Len(t, seen, 1)
	require.Equal(t, "/git/github.com/owner/repo/info/refs", seen[0].Path)
	require.Equal(t, "service=git-upload-pack", seen[0].Query)
	require.Equal(t, "Bearer person-token", seen[0].Authorization)
	require.NotContains(t, seen[0].Authorization, forwarder.token)
}

func TestCallerRouteStreamsBodiesPastTheMCPLimit(t *testing.T) {
	var received atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		n, _ := io.Copy(io.Discard, request.Body)
		received.Store(n)
	}))
	t.Cleanup(upstream.Close)
	const limit = 1 << 10
	forwarder, err := NewCredentialForwarder(
		map[string]UpstreamMCPServer{"tools": {URL: upstream.URL}},
		map[string]CallerRoute{"github.com": {URL: upstream.URL}},
		limit,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	forwarder.Bind("Bearer person-token")

	pack := strings.Repeat("x", 4*limit)
	response := callRoute(t, forwarder, http.MethodPost, forwarder.RouteURL("github.com")+"owner/repo/git-receive-pack", strings.NewReader(pack))
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.EqualValues(t, len(pack), received.Load())

	received.Store(0)
	response = callRoute(t, forwarder, http.MethodPost, forwarder.URL("tools"), strings.NewReader(pack))
	require.NotEqual(t, http.StatusOK, response.StatusCode)
	require.Less(t, received.Load(), int64(len(pack)), "an MCP body past the limit reached the upstream whole")
}

func TestCallerRouteForwardsGitHeadersOnly(t *testing.T) {
	received := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		received <- request.Header.Clone()
	}))
	t.Cleanup(upstream.Close)
	forwarder, err := NewCredentialForwarder(nil, map[string]CallerRoute{"github.com": {URL: upstream.URL}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	forwarder.Bind("Bearer person-token")

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, forwarder.RouteURL("github.com")+"owner/repo/git-upload-pack", strings.NewReader("pack"))
	require.NoError(t, err)
	for header, value := range forwarder.Headers() {
		request.Header.Set(header, value)
	}
	request.Header.Set("Git-Protocol", "version=2")
	request.Header.Set("Content-Encoding", "gzip")
	request.Header.Set("Content-Type", "application/x-git-upload-pack-request")
	request.Header.Set("Cookie", "session=claude-chosen")
	request.Header.Set("X-Forwarded-User", "someone-else")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)

	seen := <-received
	require.Equal(t, "version=2", seen.Get("Git-Protocol"))
	require.Equal(t, "gzip", seen.Get("Content-Encoding"))
	require.Equal(t, "application/x-git-upload-pack-request", seen.Get("Content-Type"))
	require.Equal(t, "Bearer person-token", seen.Get("Authorization"))
	require.Empty(t, seen.Get("Cookie"))
	require.Empty(t, seen.Get("X-Forwarded-User"))
}

func TestCallerRouteRejectsUnknownRoutesAndInputs(t *testing.T) {
	upstream, requests := newRecordingUpstream(t)
	forwarder, err := NewCredentialForwarder(nil, map[string]CallerRoute{"github.com": {URL: upstream.URL}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	forwarder.Bind("Bearer person-token")

	response := callRoute(t, forwarder, http.MethodGet, forwarder.RouteURL("gitlab.com")+"owner/repo/info/refs", nil)
	require.Equal(t, http.StatusNotFound, response.StatusCode)
	response = callRoute(t, forwarder, http.MethodGet, forwarder.URL("github.com"), nil)
	require.Equal(t, http.StatusNotFound, response.StatusCode, "a caller route answered on the MCP prefix")
	require.Empty(t, requests())

	for name, routes := range map[string]map[string]CallerRoute{
		"relative URL":  {"github.com": {URL: "/git/"}},
		"bad scheme":    {"github.com": {URL: "ssh://github.com"}},
		"name with '/'": {"github.com/x": {URL: upstream.URL}},
	} {
		_, err := NewCredentialForwarder(nil, routes, 1<<20)
		require.Error(t, err, name)
	}
}

func TestJoinPathKeepsAnAbsolutePathForAPathlessUpstream(t *testing.T) {
	for _, tc := range []struct{ upstream, suffix, want string }{
		{upstream: "http://gw:8080", suffix: "owner/repo/git-receive-pack", want: "/owner/repo/git-receive-pack"},
		{upstream: "http://gw:8080/", suffix: "owner/repo", want: "/owner/repo"},
		{upstream: "http://gw:8080/git/github.com/", suffix: "owner/repo", want: "/git/github.com/owner/repo"},
		{upstream: "http://gw:8080/mcp", suffix: "", want: "/mcp"},
	} {
		target, err := url.Parse(tc.upstream)
		require.NoError(t, err)
		path, _ := joinPath(target, tc.suffix)
		require.Equal(t, tc.want, path, "%s + %q", tc.upstream, tc.suffix)
	}
}

// TestRewriteSendsTheCredentialServeChecked pins the proxy's outgoing
// Authorization to the credential serve read, not to the forwarder's state
// when the proxy rewrites: a turn that ends or starts in between changes
// nothing about a request already admitted.
func TestRewriteSendsTheCredentialServeChecked(t *testing.T) {
	forwarder, err := NewCredentialForwarder(nil, map[string]CallerRoute{"github.com": {URL: "http://gw:8080/route/github.com/"}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	target := forwarder.routes["github.com"]

	for _, tc := range []struct{ name, checked, bound, want string }{
		{name: "turn ended after the check", checked: "Bearer person-token", bound: "", want: "Bearer person-token"},
		{name: "turn started after the check", checked: "", bound: "Bearer later-token", want: ""},
	} {
		forwarder.Bind(tc.bound)
		in := httptest.NewRequestWithContext(
			context.WithValue(t.Context(), forwardedRequestKey{}, forwardedRequest{suffix: "owner/repo/info/refs", credential: tc.checked}),
			http.MethodGet, forwarder.RouteURL("github.com")+"owner/repo/info/refs", nil,
		)
		in.Header.Set("Authorization", forwarder.Headers()["Authorization"])
		out := in.Clone(in.Context())
		forwarder.rewrite(target)(&httputil.ProxyRequest{In: in, Out: out})
		require.Equal(t, tc.want, out.Header.Get("Authorization"), tc.name)
		require.Equal(t, "/route/github.com/owner/repo/info/refs", out.URL.Path, tc.name)
	}
}

func TestClearEndsTheTurnsRequestsInFlight(t *testing.T) {
	arrived, cancelled := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		close(arrived)
		<-request.Context().Done()
		close(cancelled)
	}))
	t.Cleanup(upstream.Close)
	forwarder, err := NewCredentialForwarder(nil, map[string]CallerRoute{"github.com": {URL: upstream.URL + "/"}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })

	forwarder.Bind("Bearer person-token")
	response := callRoute(t, forwarder, http.MethodPost, forwarder.RouteURL("github.com")+"owner/repo/git-upload-pack", nil)
	require.Equal(t, http.StatusOK, response.StatusCode)
	<-arrived
	forwarder.Clear()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("a request admitted during the turn still acts as the caller after Clear")
	}
	_, err = io.ReadAll(response.Body)
	require.Error(t, err, "the client sees the stream cut, not a clean end")
}

func TestClearLeavesTheRequestsOfAServerThatOwnsItsAuthorization(t *testing.T) {
	arrived, released := make(chan struct{}), make(chan struct{})
	var authorization atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorization.Store(request.Header.Get("Authorization"))
		response.WriteHeader(http.StatusOK)
		response.(http.Flusher).Flush()
		close(arrived)
		<-released
		_, _ = io.WriteString(response, "done")
	}))
	t.Cleanup(upstream.Close)
	forwarder, err := NewCredentialForwarder(map[string]UpstreamMCPServer{
		"knowledge": {URL: upstream.URL, Headers: map[string]string{"Authorization": "gateway-placeholder"}},
	}, nil, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })

	forwarder.Bind("Bearer person-token")
	response := callRoute(t, forwarder, http.MethodPost, forwarder.URL("knowledge"), strings.NewReader("{}"))
	require.Equal(t, http.StatusOK, response.StatusCode)
	<-arrived
	require.Equal(t, "gateway-placeholder", authorization.Load())
	forwarder.Clear()
	close(released)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err, "a request that never carried the caller's credential is not the turn's to end")
	require.Equal(t, "done", string(body))
}
