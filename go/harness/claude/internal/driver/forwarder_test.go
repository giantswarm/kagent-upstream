package driver

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/stretchr/testify/require"
)

type recordedRequest struct {
	Path, Query, Authorization, Toolset, Host string
	Header                                    http.Header
}

func newRecordingUpstream(t *testing.T) (*httptest.Server, func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		seen = append(seen, recordedRequest{
			Path: request.URL.Path, Query: request.URL.RawQuery, Host: request.Host,
			Authorization: request.Header.Get("Authorization"), Toolset: request.Header.Get("X-Muster-Toolset"),
			Header: request.Header.Clone(),
		})
		mu.Unlock()
		_, _ = io.WriteString(response, "ok")
	}))
	t.Cleanup(server.Close)
	return server, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), seen...)
	}
}

func callForwarder(t *testing.T, forwarder *CredentialForwarder, url string, authorized bool) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0"}`))
	require.NoError(t, err)
	if authorized {
		for header, value := range forwarder.Headers() {
			request.Header.Set(header, value)
		}
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestCredentialForwarderCarriesTheTurnCredentialOnly(t *testing.T) {
	upstream, requests := newRecordingUpstream(t)
	forwarder, err := NewCredentialForwarder(map[string]UpstreamMCPServer{
		"muster": {URL: upstream.URL + "/mcp?tenant=lab", Headers: map[string]string{
			"X-Muster-Toolset": "preset:read-only", "authorization": "Bearer static-service",
		}},
	}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	require.Equal(t, []string{"muster"}, forwarder.Names())
	require.True(t, strings.HasPrefix(forwarder.URL("muster"), "http://127.0.0.1:"), "URL() = %q, want a loopback address", forwarder.URL("muster"))

	require.Equal(t, http.StatusUnauthorized, callForwarder(t, forwarder, forwarder.URL("muster"), false).StatusCode)
	require.Empty(t, requests(), "an unauthenticated loopback call reached the upstream")

	forwarder.Bind("Bearer person-token")
	require.Equal(t, http.StatusOK, callForwarder(t, forwarder, forwarder.URL("muster"), true).StatusCode)
	forwarder.Clear()
	require.Equal(t, http.StatusOK, callForwarder(t, forwarder, forwarder.URL("muster")+"/session?id=7", true).StatusCode)

	host := strings.TrimPrefix(upstream.URL, "http://")
	seen := requests()
	for i := range seen {
		seen[i].Header = nil
	}
	require.Equal(t, []recordedRequest{
		{Path: "/mcp", Query: "tenant=lab", Authorization: "Bearer person-token", Toolset: "preset:read-only", Host: host},
		{Path: "/mcp/session", Query: "tenant=lab&id=7", Toolset: "preset:read-only", Host: host},
	}, seen, "the static Authorization and the loopback token must never leave the process")
}

func TestCredentialForwarderRejectsUnknownServersAndInputs(t *testing.T) {
	upstream, requests := newRecordingUpstream(t)
	forwarder, err := NewCredentialForwarder(map[string]UpstreamMCPServer{"tools": {URL: upstream.URL}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	require.Equal(t, http.StatusNotFound, callForwarder(t, forwarder, forwarder.URL("other"), true).StatusCode)
	require.Empty(t, requests(), "an unknown server name reached the upstream")
	for name, servers := range map[string]map[string]UpstreamMCPServer{
		"none":          {},
		"relative URL":  {"tools": {URL: "/mcp"}},
		"bad scheme":    {"tools": {URL: "ftp://mcp.example.com"}},
		"name with '/'": {"a/b": {URL: upstream.URL}},
	} {
		_, err := NewCredentialForwarder(servers, 1<<20)
		require.Error(t, err, name)
	}
}

func TestCredentialForwarderStreamsResponses(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := response.(http.Flusher)
		if !ok {
			t.Error("upstream response is not flushable")
			return
		}
		_, _ = io.WriteString(response, "event: message\ndata: first\n\n")
		flusher.Flush()
		<-release
		_, _ = io.WriteString(response, "event: message\ndata: second\n\n")
	}))
	t.Cleanup(upstream.Close)
	forwarder, err := NewCredentialForwarder(map[string]UpstreamMCPServer{"tools": {URL: upstream.URL}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })

	response := callForwarder(t, forwarder, forwarder.URL("tools"), true)
	require.Equal(t, http.StatusOK, response.StatusCode)
	reader := bufio.NewReader(response.Body)
	first := make(chan string, 1)
	go func() {
		line, _ := reader.ReadString('\n')
		first <- line
	}()
	// Real loopback sockets keep this test outside synctest.
	select {
	case line := <-first:
		require.Equal(t, "event: message\n", line)
	case <-time.After(5 * time.Second):
		t.Fatal("the forwarder buffered the stream until the upstream finished")
	}
	close(release)
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Contains(t, string(rest), "data: second")
}

func TestCallerCredentialReadsTheA2ACall(t *testing.T) {
	require.Empty(t, CallerCredential(t.Context()))
	ctx, _ := a2asrv.NewCallContext(t.Context(), a2asrv.NewServiceParams(map[string][]string{"x-user-id": {"someone"}}))
	require.Empty(t, CallerCredential(ctx))
	ctx, _ = a2asrv.NewCallContext(t.Context(), a2asrv.NewServiceParams(map[string][]string{"authorization": {" Bearer person "}}))
	require.Equal(t, "Bearer person", CallerCredential(ctx))
}

func TestCredentialForwarderKeepsThePathAbsoluteForAPathlessUpstream(t *testing.T) {
	upstream, requests := newRecordingUpstream(t)
	forwarder, err := NewCredentialForwarder(map[string]UpstreamMCPServer{"tools": {URL: upstream.URL}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	forwarder.Bind("Bearer person-token")
	require.Equal(t, http.StatusOK, callForwarder(t, forwarder, forwarder.URL("tools")+"/session", true).StatusCode)
	seen := requests()
	require.Len(t, seen, 1)
	require.Equal(t, "/session", seen[0].Path)
}

func TestCredentialForwarderForwardsTransportHeadersOnly(t *testing.T) {
	upstream, requests := newRecordingUpstream(t)
	forwarder, err := NewCredentialForwarder(map[string]UpstreamMCPServer{
		"muster": {URL: upstream.URL + "/mcp", Headers: map[string]string{"X-Muster-Toolset": "preset:read-only"}},
	}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	forwarder.Bind("Bearer person-token")

	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, forwarder.URL("muster"), strings.NewReader(`{"jsonrpc":"2.0"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", forwarder.Headers()["Authorization"])
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Session-Id", "session-1")
	request.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	request.Header.Set("Traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	request.Header.Set("X-Muster-Toolset", "*")
	request.Header.Set("Cookie", "session=other")
	request.Header.Set("X-Custom", "value")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	require.Equal(t, http.StatusOK, response.StatusCode)

	seen := requests()
	require.Len(t, seen, 1)
	header := seen[0].Header
	require.Equal(t, "Bearer person-token", header.Get("Authorization"))
	require.Equal(t, "preset:read-only", header.Get("X-Muster-Toolset"), "Claude must not choose the toolset")
	require.Equal(t, "application/json", header.Get("Content-Type"))
	require.Equal(t, "application/json, text/event-stream", header.Get("Accept"))
	require.Equal(t, "session-1", header.Get("Mcp-Session-Id"))
	require.Equal(t, "2025-06-18", header.Get("Mcp-Protocol-Version"))
	require.NotEmpty(t, header.Get("Traceparent"))
	require.Empty(t, header.Get("Cookie"))
	require.Empty(t, header.Get("X-Custom"))
}
