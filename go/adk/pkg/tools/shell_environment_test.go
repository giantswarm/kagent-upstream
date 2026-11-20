package tools

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfiguredGitHubCLIProtocol(t *testing.T) {
	binary := os.Getenv("KAGENT_GH_TEST_BINARY")
	if binary == "" {
		t.Skip("KAGENT_GH_TEST_BINARY must name the pinned gh binary")
	}
	t.Setenv("GH_ENTERPRISE_TOKEN", "runtime-secret")
	requests := make(chan *http.Request, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(t.Context())
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"login":"fixture"}`)
	}))
	defer server.Close()
	certificate := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600))
	environment := map[string]string{
		"GH_ENTERPRISE_TOKEN": "github-sentinel", "GH_HOST": strings.TrimPrefix(server.URL, "https://"),
		"SSL_CERT_FILE": certificate, "GH_CONFIG_DIR": t.TempDir(), "HOME": t.TempDir(),
	}
	output, err := NewCommandExecutor(time.Minute, environment).ExecuteCommand(t.Context(), "'"+binary+"' api user", t.TempDir())
	require.NoError(t, err, "%s", output)
	require.Contains(t, output, `"login":"fixture"`)
	select {
	case request := <-requests:
		require.Equal(t, "/api/v3/user", request.URL.Path)
		require.Equal(t, "token github-sentinel", request.Header.Get("Authorization"))
	default:
		t.Fatal("gh sent no request")
	}
}

func TestConfiguredShellEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", "runtime-secret")
	t.Setenv("GITLAB_TOKEN", "runtime-secret")
	t.Setenv("UNDECLARED_DATABASE_URL", "runtime-secret")
	t.Setenv("KAGENT_CONFIG_JSON", "runtime-secret")
	t.Setenv("EXPLICIT_EMPTY", "runtime-secret")
	environment := map[string]string{
		"GH_TOKEN": "github-sentinel", "GITLAB_TOKEN": "gitlab-sentinel",
		"GLAB_IS_OAUTH2": "true", "GITLAB_HOST": "forge.example",
		"EXPLICIT_EMPTY": "", "WITH_EQUALS": "a=b", "PWD": "/wrong",
	}
	executor := NewCommandExecutor(time.Minute, environment)
	environment["GH_TOKEN"] = "mutated"
	directory := t.TempDir()
	output, err := executor.ExecuteCommand(t.Context(), `printf '%s\n' "$GH_TOKEN" "$GITLAB_TOKEN" "$GLAB_IS_OAUTH2" "$GITLAB_HOST" "${EXPLICIT_EMPTY-unset}" "$WITH_EQUALS" "$PWD" "${UNDECLARED_DATABASE_URL-unset}" "${KAGENT_CONFIG_JSON-unset}"`, directory)
	require.NoError(t, err)
	require.Equal(t, "github-sentinel\ngitlab-sentinel\ntrue\nforge.example\n\na=b\n"+directory+"\nunset\nunset", output)
}

func TestConfiguredShellProtocolRequest(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is required")
	}
	t.Setenv("GH_TOKEN", "runtime-secret")
	t.Setenv("UNDECLARED_TOKEN", "runtime-secret")
	requests := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Header.Clone()
		fmt.Fprint(writer, "accepted")
	}))
	defer server.Close()
	output, err := NewCommandExecutor(time.Minute, map[string]string{"GH_TOKEN": "protocol-sentinel"}).ExecuteCommand(t.Context(),
		`curl --fail --silent --noproxy '*' -H "Authorization: Bearer $GH_TOKEN" -H "X-Undeclared: ${UNDECLARED_TOKEN-unset}" `+server.URL, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "accepted", output)
	select {
	case headers := <-requests:
		require.Equal(t, "Bearer protocol-sentinel", headers.Get("Authorization"))
		require.Equal(t, "unset", headers.Get("X-Undeclared"))
	default:
		t.Fatal("shell tool sent no request")
	}
}

func TestConfiguredGitLabCLIProtocol(t *testing.T) {
	binary := os.Getenv("KAGENT_GLAB_TEST_BINARY")
	if binary == "" {
		t.Skip("KAGENT_GLAB_TEST_BINARY must name the pinned glab binary")
	}
	t.Setenv("GITLAB_TOKEN", "runtime-secret")
	for _, oauth := range []bool{false, true} {
		t.Run(fmt.Sprintf("oauth=%t", oauth), func(t *testing.T) {
			requests := make(chan *http.Request, 1)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests <- request.Clone(t.Context())
				writer.Header().Set("Content-Type", "application/json")
				fmt.Fprint(writer, `{"username":"fixture"}`)
			}))
			defer server.Close()
			environment := map[string]string{
				"GITLAB_TOKEN": "gitlab-sentinel", "GLAB_IS_OAUTH2": fmt.Sprint(oauth),
				"GITLAB_HOST": strings.TrimPrefix(server.URL, "http://"), "GLAB_API_PROTOCOL": "http",
				"GLAB_CONFIG_DIR": t.TempDir(), "HOME": t.TempDir(), "GLAB_CHECK_UPDATE": "false",
			}
			output, err := NewCommandExecutor(time.Minute, environment).ExecuteCommand(t.Context(), "'"+binary+"' api user", t.TempDir())
			require.NoError(t, err, "%s", output)
			require.Contains(t, output, `"username":"fixture"`)
			select {
			case request := <-requests:
				require.Equal(t, "/api/v4/user", request.URL.Path)
				if oauth {
					require.Equal(t, "Bearer gitlab-sentinel", request.Header.Get("Authorization"))
					require.Empty(t, request.Header.Get("Private-Token"))
				} else {
					require.Equal(t, "gitlab-sentinel", request.Header.Get("Private-Token"))
					require.Empty(t, request.Header.Get("Authorization"))
				}
			default:
				t.Fatal("glab sent no request")
			}
		})
	}
}

func TestConfiguredGitHelperProtocol(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "credential.helper")
	t.Setenv("GIT_CONFIG_VALUE_0", "runtime-secret")
	credentials := make(chan [2]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		username, password, authenticated := request.BasicAuth()
		if !authenticated {
			writer.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		credentials <- [2]string{username, password}
		writer.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		service := "# service=git-upload-pack\n"
		fmt.Fprintf(writer, "%04x%s00000000", len(service)+4, service)
	}))
	defer server.Close()
	environment := map[string]string{
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "credential.helper",
		"GIT_CONFIG_VALUE_0": "!f() { test \"$1\" = get && printf 'username=fixture\\npassword=git-sentinel\\n'; }; f",
		"GIT_CONFIG_GLOBAL":  os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0", "HOME": t.TempDir(),
	}
	output, err := NewCommandExecutor(time.Minute, environment).ExecuteCommand(t.Context(), "git ls-remote "+server.URL+"/repository.git", t.TempDir())
	require.NoError(t, err, "%s", output)
	select {
	case credential := <-credentials:
		require.Equal(t, [2]string{"fixture", "git-sentinel"}, credential)
	default:
		t.Fatal("git sent no authenticated request")
	}
}
