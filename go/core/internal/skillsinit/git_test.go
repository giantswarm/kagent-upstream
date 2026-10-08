package skillsinit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test_applySubPath_rejectsTraversal exercises the validation gate without
// invoking `cp`. We give it a clean dest tree with a real subdir then ask
// for traversal — the function must error before touching the filesystem.
func Test_applySubPath_rejectsTraversal(t *testing.T) {
	dest := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dest, "real"), 0o755))

	cases := []string{
		"../escape",
		"/etc",
		"a/../../escape",
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			err := applySubPath(dest, p)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid subPath")
		})
	}
}

// Test_applySubPath_rejectsNonDir guards against a benign-looking subPath
// that points at a file rather than a directory. Without this check the
// subsequent `cp -rL` would do something silly; the explicit error is
// clearer and matches the documented contract.
func Test_applySubPath_rejectsNonDir(t *testing.T) {
	dest := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dest, "file"), []byte("x"), 0o644))

	err := applySubPath(dest, "file")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

func TestCloneGitCommitRejectsMutableRef(t *testing.T) {
	err := CloneGitCommit("https://example.com/repository.git", "main", t.TempDir(), false)
	require.ErrorContains(t, err, "full SHA")
}

// TestCloneGitCommitAuthorizationPlaceholder guards the egress contract: the
// gateway replaces an Authorization header the request carries and adds none,
// so an authenticated source must send the placeholder on its first request,
// and a public source must send nothing. The server answers 401 like a private
// repository without a credential; git must fail without prompting.
func TestCloneGitCommitAuthorizationPlaceholder(t *testing.T) {
	for _, authenticated := range []bool{true, false} {
		t.Run(fmt.Sprintf("authenticated=%t", authenticated), func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Header.Get("Authorization"))
				mu.Unlock()
				w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer server.Close()

			err := CloneGitCommit(server.URL+"/org/private", strings.Repeat("a", 40), filepath.Join(t.TempDir(), "dest"), authenticated)
			require.Error(t, err)

			mu.Lock()
			defer mu.Unlock()
			require.NotEmpty(t, seen)
			want := ""
			if authenticated {
				want = credentialPlaceholder
			}
			assert.Equal(t, want, seen[0])
		})
	}
}

func TestGitEnvironmentScopesThePlaceholderToTheSource(t *testing.T) {
	assert.Equal(t, []string{"GIT_TERMINAL_PROMPT=0"}, gitEnvironment("https://github.com/org/public", false))
	assert.Equal(t, []string{
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.https://github.com/org/private.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic kagent-credential-injected",
	}, gitEnvironment("https://github.com/org/private", true))
}

// TestCloneGit_fullCheckoutBySHA guards #2608: a Full clone with a commit
// SHA ref must check the SHA out as a revision. Passing it after `--`
// made git treat it as a pathspec and fail with "did not match any
// file(s) known to git". The second commit makes the test sensitive to
// the checkout step itself: a clone that merely succeeded but left HEAD
// at the branch tip would still contain "f" but not be at the SHA.
func TestCloneGit_fullCheckoutBySHA(t *testing.T) {
	src := t.TempDir()
	gitIn(t, src, "init", "-q")
	gitIn(t, src, "config", "user.email", "t@example.com")
	gitIn(t, src, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(src, "f"), []byte("x"), 0o644))
	gitIn(t, src, "add", "f")
	gitIn(t, src, "commit", "-qm", "init")
	sha := gitOut(t, src, "rev-parse", "HEAD")
	require.Len(t, sha, 40)
	gitIn(t, src, "commit", "-qm", "second", "--allow-empty")

	dest := filepath.Join(t.TempDir(), "dest")
	require.NoError(t, CloneGit(GitRef{URL: src, Ref: sha, Dest: dest, Full: true}))
	got, err := os.ReadFile(filepath.Join(dest, "f"))
	require.NoError(t, err)
	assert.Equal(t, []byte("x"), got)
	assert.Equal(t, sha, gitOut(t, dest, "rev-parse", "HEAD"))
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}
