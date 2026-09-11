package skillsinit

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	immutableGitCommit = regexp.MustCompile(`^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)
	environmentName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	credentialUsername = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// DefaultGitUsername accompanies a token when the source does not name one.
// GitHub and GitLab accept any username with a personal access token; GitHub
// App installation tokens expect this one.
const DefaultGitUsername = "x-access-token"

// GitCredential names the environment variable that holds a token for one
// source host. The token itself never passes through this package: git reads
// it from the environment through a credential helper, and only after the
// host has asked for authentication, so a public repository on the same host
// never sees it.
type GitCredential struct {
	// Username is presented with the token. Empty selects DefaultGitUsername.
	Username string
	// TokenEnv is the name of the environment variable holding the token.
	TokenEnv string
}

// CloneGit fetches a single git ref into ref.Dest. All user-controlled
// strings (URL, Ref, SubPath) are passed to git as separate argv entries via
// exec.Command — they never pass through a shell, so metacharacters in any of
// them are inert.
//
// When ref.Full is true we do a full clone then `git checkout <sha>`,
// because shallow `--branch` does not accept commit SHAs. When false we use a
// depth-1 branch/tag clone.
//
// SubPath, if set, rewrites the destination so the final layout matches the
// requested in-repo subdirectory.
func CloneGit(ref GitRef) error {
	if ref.Full {
		if err := runGit("clone", "--", ref.URL, ref.Dest); err != nil {
			return err
		}
		// `--` separator prevents a ref starting with `-` from being parsed
		// as a flag. Refs are already validated upstream as 40-char hex when
		// Full is true, but defense in depth costs nothing.
		if err := runGitIn(ref.Dest, "checkout", "--", ref.Ref); err != nil {
			return err
		}
	} else {
		if err := runGit("clone", "--depth", "1", "--branch", ref.Ref, "--", ref.URL, ref.Dest); err != nil {
			return err
		}
	}

	if ref.SubPath != "" {
		if err := applySubPath(ref.Dest, ref.SubPath); err != nil {
			return fmt.Errorf("apply subPath %q: %w", ref.SubPath, err)
		}
	}
	return nil
}

// CloneGitCommit fetches only one immutable commit instead of cloning the
// repository's complete history. A nil credential fetches anonymously.
func CloneGitCommit(url, commit, destination string, credential *GitCredential) error {
	if !immutableGitCommit.MatchString(commit) {
		return fmt.Errorf("git commit must be a full SHA")
	}
	environment, err := gitEnvironment(url, credential)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	if err := runGitWith(destination, environment, "init"); err != nil {
		return err
	}
	if err := runGitWith(destination, environment, "remote", "add", "origin", url); err != nil {
		return err
	}
	if err := runGitWith(destination, environment, "fetch", "--depth", "1", "origin", commit); err != nil {
		return err
	}
	return runGitWith(destination, environment, "checkout", "--detach", "FETCH_HEAD")
}

// gitEnvironment builds the environment for one fetch. Git is configured
// through GIT_CONFIG_COUNT/KEY/VALUE (git 2.31+), so nothing is written to a
// gitconfig or a credentials file that a filesystem snapshot could capture.
// Terminal prompts are disabled: a headless fetch must fail, never hang.
func gitEnvironment(rawURL string, credential *GitCredential) ([]string, error) {
	environment := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_CONFIG_") || strings.HasPrefix(entry, "GIT_TERMINAL_PROMPT=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GIT_TERMINAL_PROMPT=0")
	if credential == nil {
		return environment, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("a git credential requires an https URL without user information, got %q", rawURL)
	}
	if !environmentName.MatchString(credential.TokenEnv) {
		return nil, fmt.Errorf("git credential environment variable name %q is invalid", credential.TokenEnv)
	}
	if value, ok := os.LookupEnv(credential.TokenEnv); !ok || value == "" {
		return nil, fmt.Errorf("git credential environment variable %q for %s is not set", credential.TokenEnv, parsed.Host)
	}
	username := credential.Username
	if username == "" {
		username = DefaultGitUsername
	}
	if !credentialUsername.MatchString(username) {
		return nil, fmt.Errorf("git credential username %q is invalid", username)
	}
	// The helper is run by git through /bin/sh. It carries the variable's
	// name only; the shell expands the token when the host challenges.
	helper := fmt.Sprintf(`!f() { echo "username=%s"; echo "password=$%s"; }; f`, username, credential.TokenEnv)
	scope := parsed.Scheme + "://" + parsed.Host
	return append(environment,
		"GIT_CONFIG_COUNT=2",
		// Reset inherited helpers first so only the scoped one answers.
		"GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential."+scope+".helper", "GIT_CONFIG_VALUE_1="+helper,
	), nil
}

func runGit(args ...string) error {
	return runGitIn("", args...)
}

func runGitIn(dir string, args ...string) error {
	return runGitWith(dir, os.Environ(), args...)
}

func runGitWith(dir string, environment []string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = environment
	return cmd.Run()
}

// applySubPath replaces dest with the contents of dest/subPath. The result is
// that dest contains only the requested subdirectory. We materialize the
// content into a sibling tmp dir under the same parent so the final rename is
// atomic on the same filesystem.
func applySubPath(dest, subPath string) error {
	// Defense in depth: filepath.IsLocal rejects absolute paths, ".."
	// segments, and reserved names — exactly the set we want to refuse.
	if !filepath.IsLocal(subPath) {
		return fmt.Errorf("invalid subPath %q", subPath)
	}
	clean := filepath.Clean(subPath)
	src := filepath.Join(dest, clean)
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat subPath: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("subPath %q is not a directory", subPath)
	}

	parent := filepath.Dir(dest)
	tmp, err := os.MkdirTemp(parent, ".skill-subpath-*")
	if err != nil {
		return fmt.Errorf("mktemp: %w", err)
	}
	// Best-effort cleanup if we fail before the rename.
	cleanupTmp := tmp
	defer func() {
		if cleanupTmp != "" {
			os.RemoveAll(cleanupTmp)
		}
	}()

	// cp -rL: follow symlinks (matches the original behavior); both paths
	// are constructed by us, never user-supplied, and are passed as argv —
	// no shell, no metacharacter risk. Trailing "/." copies *contents* of
	// src into tmp, not src itself.
	cp := exec.Command("cp", "-rL", "--", src+"/.", tmp)
	cp.Stdout = os.Stdout
	cp.Stderr = os.Stderr
	if err := cp.Run(); err != nil {
		return fmt.Errorf("cp subPath contents: %w", err)
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return err
	}
	cleanupTmp = ""
	return nil
}
