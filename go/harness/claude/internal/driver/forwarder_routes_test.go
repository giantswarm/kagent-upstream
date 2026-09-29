package driver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

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
