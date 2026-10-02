package adapter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/harness/claude/config"
	"github.com/kagent-dev/kagent/go/harness/claude/internal/driver"
)

func TestCallerRoutesParsesTheHarnessEnvironment(t *testing.T) {
	routes, err := callerRoutes([]string{"PATH=/bin"})
	require.NoError(t, err)
	require.Empty(t, routes)

	routes, err = callerRoutes([]string{config.CallerRoutesEnvName + `={"github.com":"http://gw:8080/git/github.com/","gitlab.example.com:8443":"http://gw:8080/git/gitlab/"}`})
	require.NoError(t, err)
	require.Equal(t, map[string]driver.CallerRoute{
		"github.com":              {URL: "http://gw:8080/git/github.com/"},
		"gitlab.example.com:8443": {URL: "http://gw:8080/git/gitlab/"},
	}, routes)

	for name, value := range map[string]string{
		"not JSON":         `github.com=http://gw`,
		"scheme in host":   `{"https://github.com":"http://gw"}`,
		"path in host":     `{"github.com/org":"http://gw"}`,
		"uppercase host":   `{"GitHub.com":"http://gw"}`,
		"URL not a string": `{"github.com":1}`,
	} {
		_, err := callerRoutes([]string{config.CallerRoutesEnvName + "=" + value})
		require.Error(t, err, name)
	}
}

func TestNewRequiresCallerTokenPropagationForRoutes(t *testing.T) {
	durableDir := filepath.Join(t.TempDir(), "data")
	cfg := config.Production("claude-test", "help")
	cfg.StrictVersion = false
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	_, err = New(t.Context(), Input{
		ConfigJSON: raw, Workspace: filepath.Join(durableDir, "workspace"), DurableDir: durableDir,
		EphemeralDir: filepath.Join(t.TempDir(), "generated"),
		Environment:  []string{config.CallerRoutesEnvName + `={"github.com":"http://gw:8080/git/github.com/"}`},
	})
	require.ErrorContains(t, err, config.PropagateTokenEnvName+"=true: a caller route acts as the turn's caller")
}

func TestNewRefusesCallerRoutesUnlessClaudeRunsAsAnotherUser(t *testing.T) {
	skipAsRoot(t)
	durableDir := filepath.Join(t.TempDir(), "data")
	cfg := config.Production("claude-test", "help")
	cfg.StrictVersion = false
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	_, err = New(t.Context(), Input{
		ConfigJSON: raw, Workspace: filepath.Join(durableDir, "workspace"), DurableDir: durableDir,
		EphemeralDir: filepath.Join(t.TempDir(), "generated"),
		Environment: []string{
			config.PropagateTokenEnvName + "=true",
			config.CallerRoutesEnvName + `={"github.com":"http://gw:8080/git/github.com/"}`,
		},
	})
	require.ErrorContains(t, err, "KAGENT_PROPAGATE_TOKEN requires the harness to run as root",
		"routes without MCP servers still hold the caller's credential")
}

func TestGitRouteEnvironmentRefusesAnExistingGitConfigCount(t *testing.T) {
	forwarder, err := driver.NewCredentialForwarder(nil, map[string]driver.CallerRoute{"github.com": {URL: "http://gw:8080/"}}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	_, err = gitRouteEnvironment([]string{"GIT_CONFIG_COUNT=1"}, forwarder, []string{"github.com"})
	require.ErrorContains(t, err, "GIT_CONFIG_COUNT")
}

// TestGitReachesTheRouteAsTheCaller runs the real git client with the
// environment the adapter hands Claude: https://github.com/ must reach the
// route's upstream with the turn caller's credential and nothing else.
func TestGitReachesTheRouteAsTheCaller(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	var mu sync.Mutex
	var paths, authorizations []string
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		paths = append(paths, request.URL.Path+"?"+request.URL.RawQuery)
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		mu.Unlock()
		http.Error(response, "not a git server", http.StatusNotFound)
	}))
	t.Cleanup(upstream.Close)
	forwarder, err := driver.NewCredentialForwarder(nil, map[string]driver.CallerRoute{
		"github.com": {URL: upstream.URL + "/git/github.com/"},
	}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })

	environment, err := gitRouteEnvironment([]string{"HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0"}, forwarder, []string{"github.com"})
	require.NoError(t, err)
	environment = append(environment, "GIT_CONFIG_GLOBAL=/dev/null")

	forwarder.Bind("Bearer person-token")
	command := exec.CommandContext(t.Context(), gitPath, "ls-remote", "https://github.com/owner/repo")
	command.Env = environment
	command.Dir = t.TempDir()
	output, err := command.CombinedOutput()
	require.Error(t, err, "the fake upstream is not a git server: %s", output)

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, paths, "git never reached the route: %s", output)
	require.Equal(t, "/git/github.com/owner/repo/info/refs?service=git-upload-pack", paths[0])
	for _, authorization := range authorizations {
		require.Equal(t, "Bearer person-token", authorization)
	}
	require.NotContains(t, strings.Join(environment, "\n"), "person-token", "the caller's credential entered Claude's environment")
}

// TestGitRewritesOnlyTheRoutedHosts asks the real git client how it resolves
// remote URLs under the adapter's environment: only https://<host>/ of a
// routed host reaches the forwarder, and the loopback token is sent nowhere
// else.
func TestGitRewritesOnlyTheRoutedHosts(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	forwarder, err := driver.NewCredentialForwarder(nil, map[string]driver.CallerRoute{
		"github.com":              {URL: "http://gw:8080/route/github.com/"},
		"gitlab.example.com:8443": {URL: "http://gw:8080/route/gitlab.example.com:8443/"},
	}, 1<<20)
	require.NoError(t, err)
	t.Cleanup(func() { _ = forwarder.Close() })
	environment, err := gitRouteEnvironment([]string{"HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}, forwarder, []string{"github.com", "gitlab.example.com:8443"})
	require.NoError(t, err)
	// Outside any repository: a checkout's own config (CI writes an
	// extraheader for https://github.com/ there) would answer for the remotes.
	outside := t.TempDir()
	git := func(args ...string) string {
		command := exec.CommandContext(t.Context(), gitPath, args...)
		command.Env = environment
		command.Dir = outside
		output, _ := command.Output()
		return strings.TrimSpace(string(output))
	}

	require.Equal(t, forwarder.RouteURL("github.com")+"owner/repo", git("ls-remote", "--get-url", "https://github.com/owner/repo"))
	require.Equal(t, forwarder.RouteURL("gitlab.example.com:8443")+"group/repo", git("ls-remote", "--get-url", "https://gitlab.example.com:8443/group/repo"))
	for _, remote := range []string{
		"https://github.com.evil.example/owner/repo",
		"https://api.github.com/owner/repo",
		"https://gitlab.com/owner/repo",
		"https://gitlab.example.com/group/repo",
		"https://GitHub.com/owner/repo",
		"https://github.com:443/owner/repo",
		"http://github.com/owner/repo",
		"git@github.com:owner/repo",
	} {
		require.Equal(t, remote, git("ls-remote", "--get-url", remote), "git rewrote a remote outside the routes")
		require.Empty(t, git("config", "--get-urlmatch", "http.extraHeader", remote), "the loopback token would reach %s", remote)
	}
	require.Equal(t, "Authorization: "+forwarder.Headers()["Authorization"], git("config", "--get-urlmatch", "http.extraHeader", forwarder.RouteURL("github.com")))
}
