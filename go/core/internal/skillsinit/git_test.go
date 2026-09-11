package skillsinit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	err := CloneGitCommit("https://example.com/repository.git", "main", t.TempDir(), nil)
	require.ErrorContains(t, err, "full SHA")
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

func TestGitEnvironmentAnonymousFetchDisablesPrompts(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "credential.helper")
	t.Setenv("GIT_CONFIG_VALUE_0", "store")
	environment, err := gitEnvironment("https://github.com/acme/skills", nil)
	require.NoError(t, err)
	assert.Contains(t, environment, "GIT_TERMINAL_PROMPT=0")
	for _, entry := range environment {
		assert.False(t, strings.HasPrefix(entry, "GIT_CONFIG_"), "inherited git configuration must not leak into the fetch: %s", entry)
	}
}

func TestGitEnvironmentScopesTheHelperToTheSourceHost(t *testing.T) {
	const token = "ghp_secret-value"
	t.Setenv("SKILL_TOKEN", token)
	environment, err := gitEnvironment("https://github.com/acme/private-skills", &GitCredential{TokenEnv: "SKILL_TOKEN"})
	require.NoError(t, err)
	assert.Contains(t, environment, "GIT_CONFIG_COUNT=2")
	assert.Contains(t, environment, "GIT_CONFIG_KEY_0=credential.helper")
	assert.Contains(t, environment, "GIT_CONFIG_VALUE_0=")
	assert.Contains(t, environment, "GIT_CONFIG_KEY_1=credential.https://github.com.helper")
	assert.Contains(t, environment, `GIT_CONFIG_VALUE_1=!f() { echo "username=x-access-token"; echo "password=$SKILL_TOKEN"; }; f`)
	for _, entry := range environment {
		if strings.HasPrefix(entry, "SKILL_TOKEN=") {
			continue
		}
		assert.NotContains(t, entry, token, "the token must reach git only through its own variable")
	}
}

func TestGitEnvironmentHonoursTheUsername(t *testing.T) {
	t.Setenv("SKILL_TOKEN", "glpat-value")
	environment, err := gitEnvironment("https://gitlab.example.com/group/skills.git", &GitCredential{Username: "deploy-token-1", TokenEnv: "SKILL_TOKEN"})
	require.NoError(t, err)
	assert.Contains(t, environment, "GIT_CONFIG_KEY_1=credential.https://gitlab.example.com.helper")
	assert.Contains(t, environment, `GIT_CONFIG_VALUE_1=!f() { echo "username=deploy-token-1"; echo "password=$SKILL_TOKEN"; }; f`)
}

func TestGitEnvironmentRejectsAnUnsetToken(t *testing.T) {
	_, err := gitEnvironment("https://github.com/acme/private-skills", &GitCredential{TokenEnv: "UNSET_SKILL_TOKEN_FOR_TEST"})
	require.ErrorContains(t, err, `environment variable "UNSET_SKILL_TOKEN_FOR_TEST" for github.com is not set`)
}

func TestGitEnvironmentRejectsUnsafeInputs(t *testing.T) {
	t.Setenv("SKILL_TOKEN", "value")
	cases := map[string]struct {
		url        string
		credential GitCredential
		want       string
	}{
		"http url":        {"http://github.com/acme/skills", GitCredential{TokenEnv: "SKILL_TOKEN"}, "requires an https URL"},
		"userinfo in url": {"https://user@github.com/acme/skills", GitCredential{TokenEnv: "SKILL_TOKEN"}, "requires an https URL"},
		"variable name":   {"https://github.com/acme/skills", GitCredential{TokenEnv: "SKILL TOKEN"}, "variable name"},
		"username":        {"https://github.com/acme/skills", GitCredential{Username: `x"; rm -rf /; echo "`, TokenEnv: "SKILL_TOKEN"}, "username"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := gitEnvironment(tc.url, &tc.credential)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// git itself must accept the per-invocation configuration; this runs the real
// binary without any network access.
func TestGitEnvironmentIsAcceptedByGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("SKILL_TOKEN", "value")
	environment, err := gitEnvironment("https://github.com/acme/private-skills", &GitCredential{TokenEnv: "SKILL_TOKEN"})
	require.NoError(t, err)
	cmd := exec.Command("git", "config", "--get", "credential.https://github.com.helper")
	cmd.Env = environment
	cmd.Dir = t.TempDir()
	output, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, `!f() { echo "username=x-access-token"; echo "password=$SKILL_TOKEN"; }; f`, strings.TrimSpace(string(output)))
}
