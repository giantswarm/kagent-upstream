package skillsinit

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

var immutableGitCommit = regexp.MustCompile(`^([0-9a-fA-F]{40}|[0-9a-fA-F]{64})$`)

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
		// The trailing `--` separates revisions from pathspecs: without
		// it, git treats the ref as a pathspec and the checkout fails
		// for commit SHAs. It does not guard against a ref that looks
		// like an option — refs are already validated upstream as
		// 40-char hex when Full is true.
		if err := runGitIn(ref.Dest, "checkout", ref.Ref, "--"); err != nil {
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

// credentialPlaceholder is the Authorization value git sends for an
// authenticated source. The egress gateway replaces the whole header with the
// source's credential and never inspects the placeholder.
const credentialPlaceholder = "Basic kagent-credential-injected"

// CloneGitCommit fetches only one immutable commit instead of cloning the
// repository's complete history. An authenticated source sends a placeholder
// Authorization header for its URL only: the egress gateway replaces a
// credential header a request carries and leaves a request without one
// untouched, and git sends none of its own before a challenge.
func CloneGitCommit(url, commit, destination string, authenticated bool) error {
	if !immutableGitCommit.MatchString(commit) {
		return fmt.Errorf("git commit must be a full SHA")
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	env := gitEnvironment(url, authenticated)
	if err := runGitWith(destination, env, "init"); err != nil {
		return err
	}
	if err := runGitWith(destination, env, "remote", "add", "origin", url); err != nil {
		return err
	}
	if err := runGitRetrying(destination, env, "fetch", "--depth", "1", "origin", commit); err != nil {
		return err
	}
	return runGitWith(destination, env, "checkout", "--detach", "FETCH_HEAD")
}

// gitEnvironment returns the environment additions for fetching url. Git never
// prompts: a refused credential fails with its error instead of waiting on a
// terminal the actor does not have. An authenticated source gets the
// placeholder header through git's per-invocation configuration, scoped to its
// URL and never written to a gitconfig.
func gitEnvironment(url string, authenticated bool) []string {
	env := []string{"GIT_TERMINAL_PROMPT=0"}
	if authenticated {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http."+url+".extraHeader",
			"GIT_CONFIG_VALUE_0=Authorization: "+credentialPlaceholder,
		)
	}
	return env
}

func runGit(args ...string) error {
	return runGitIn("", args...)
}

func runGitIn(dir string, args ...string) error {
	return runGitWith(dir, nil, args...)
}

func runGitWith(dir string, env []string, args ...string) error {
	return gitCommand(dir, env, os.Stderr, args...).Run()
}

func gitCommand(dir string, env []string, stderr io.Writer, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// fetchAttempts bounds how often a network step of git runs when it fails on
// a transient network error; fetchBackoff is the wait before the second
// attempt, doubled before each later one (1 s, 2 s, 4 s).
var (
	fetchAttempts = 4
	fetchBackoff  = time.Second
)

// transientGitFailure matches git's error for a failure a later attempt can
// outlive: the name does not resolve yet, the connection is refused, times out
// or drops, or a gateway or server answers 5xx. A refused credential (401,
// 403), a missing repository (404) or an unknown commit matches none of them
// and fails at once.
var transientGitFailure = regexp.MustCompile(`Could not resolve host|Failed to connect to|Connection refused|Connection timed out|Operation timed out|Connection reset by peer|Empty reply from server|The requested URL returned error: 5[0-9]{2}|RPC failed; HTTP 5[0-9]{2}`)

// runGitRetrying runs a network step of git and runs it again, up to
// fetchAttempts times, while it fails on a transient network error.
func runGitRetrying(dir string, env []string, args ...string) error {
	wait := fetchBackoff
	for attempt := 1; ; attempt++ {
		var stderr bytes.Buffer
		err := gitCommand(dir, env, io.MultiWriter(os.Stderr, &stderr), args...).Run()
		if err == nil || attempt == fetchAttempts || !transientGitFailure.Match(stderr.Bytes()) {
			return err
		}
		fmt.Fprintf(os.Stderr, "git %s failed on a transient network error (attempt %d of %d), retrying in %s\n", args[0], attempt, fetchAttempts, wait)
		time.Sleep(wait)
		wait *= 2
	}
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
