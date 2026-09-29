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
	require.ErrorContains(t, err, config.PropagateTokenEnvName)

	runner, err := New(t.Context(), Input{
		ConfigJSON: raw, Workspace: filepath.Join(durableDir, "workspace"), DurableDir: durableDir,
		EphemeralDir: filepath.Join(t.TempDir(), "generated"),
		Environment: []string{
			config.PropagateTokenEnvName + "=true",
			config.CallerRoutesEnvName + `={"github.com":"http://gw:8080/git/github.com/"}`,
		},
	})
	require.NoError(t, err, "routes without MCP servers still need the forwarder")
	t.Cleanup(func() { _ = runner.Close() })
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
