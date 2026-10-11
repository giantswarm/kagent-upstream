// Package workspace sets up a session's working directory on a workspace
// volume before its first turn: a clone with alternates of every bare mirror
// the volume shares, and a fetch of exactly the repositories that moved since
// the mirrors' last sync. Every Harness runtime that works in such a directory
// uses it, so a session looks the same whichever runtime runs it.
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/kagent-dev/kagent/go/api/adk"
	"github.com/kagent-dev/kagent/go/harness/internal/utils"
)

// StateFile, relative to the working directory, records that the directory
// was set up. A directory without it is set up again, which a fresh directory
// of a resumed session needs.
const StateFile = ".kagent/workspace.json"

const (
	// DefaultFetchTimeout bounds the fetch of one repository.
	DefaultFetchTimeout = 2 * time.Minute
	// DefaultFetchBudget bounds the fetches of one setup together.
	DefaultFetchBudget = 5 * time.Minute
	// maxReasonBytes bounds the part of git's output a problem reports.
	maxReasonBytes = 512
)

// Dispatch is what a turn's dispatch says about the directory it works in.
type Dispatch struct {
	// Directory is the turn's working directory; empty when the dispatch
	// names none.
	Directory string
	// Mirrors is the read-only directory of bare mirrors,
	// <owner>/<repository>.git, its clones borrow objects from; empty when the
	// session mounts none.
	Mirrors string
	// Changed lists the repositories, <owner>/<repository>, whose origin moved
	// since the mirrors' last sync.
	Changed []string
}

// DispatchFrom reads the dispatch of the A2A call that owns ctx. A call
// without one yields the zero Dispatch.
func DispatchFrom(ctx context.Context) Dispatch {
	callCtx, ok := a2asrv.CallContextFrom(ctx)
	if !ok || callCtx == nil || callCtx.ServiceParams() == nil {
		return Dispatch{}
	}
	params := callCtx.ServiceParams()
	first := func(name string) string {
		values, ok := params.Get(name)
		if !ok || len(values) == 0 {
			return ""
		}
		return strings.TrimSpace(values[0])
	}
	dispatch := Dispatch{Directory: first(adk.WorkingDirectoryHeader), Mirrors: first(adk.MirrorsDirectoryHeader)}
	values, _ := params.Get(adk.ChangedRepositoriesHeader)
	// A proxy may join repeated values with commas; both forms parse alike.
	for _, value := range values {
		for repository := range strings.SplitSeq(value, ",") {
			// Only <owner>/<repository> names a clone under the directory.
			if repository = strings.TrimSpace(repository); validRepository(repository) && !slices.Contains(dispatch.Changed, repository) {
				dispatch.Changed = append(dispatch.Changed, repository)
			}
		}
	}
	return dispatch
}

// Options configure how a directory is set up.
type Options struct {
	// Notes is the file name, relative to the working directory, of the notes
	// that tell the agent how the directory is laid out, such as the file the
	// runtime's agent reads on start. Empty writes none.
	Notes string
	// Environment is the environment of every git command: the sandbox's own,
	// which reaches the origin through its egress.
	Environment []string
	// RunAs, when set, makes every git command run as the user the agent runs
	// as, who then owns the clones.
	RunAs *Identity
	// FetchTimeout bounds one repository's fetch, FetchBudget all of them;
	// zero takes the defaults.
	FetchTimeout, FetchBudget time.Duration
}

// Identity is a POSIX user and group.
type Identity struct {
	UID, GID uint32
}

// Problem is a repository the setup could not clone or fetch: the turn still
// runs, and the agent works with what the mirror holds.
type Problem struct {
	Repository string
	Reason     string
}

// Report is what one setup did.
type Report struct {
	// SetUp is true when this call set the directory up; false when it was
	// already, or the dispatch names no workspace.
	SetUp bool
	// Cloned lists the repositories cloned from their mirrors.
	Cloned []string
	// Fetched lists the changed repositories fetched from their origin.
	Fetched []string
	// Problems lists what could not be cloned or fetched.
	Problems []Problem
}

// Notice is the one status line a turn reports for the report's problems, or
// empty without any.
func (r Report) Notice() string {
	if len(r.Problems) == 0 {
		return ""
	}
	lines := make([]string, 0, len(r.Problems)+1)
	lines = append(lines, "Workspace setup: these repositories stay at the workspace's last sync.")
	for _, problem := range r.Problems {
		lines = append(lines, fmt.Sprintf("- %s: %s", problem.Repository, problem.Reason))
	}
	return strings.Join(lines, "\n")
}

// state is what StateFile records.
type state struct {
	SetUp    time.Time `json:"setUp"`
	Cloned   []string  `json:"cloned"`
	Fetched  []string  `json:"fetched,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
}

// Prepare sets up the dispatch's directory once: a directory that holds
// StateFile, or a dispatch without mirrors, is left as it is. Every mirror
// gets a clone with alternates at <owner>/<repository> under the directory,
// without a working tree, its origin the mirror's own origin; the changed
// repositories are then fetched from their origin. A repository that fails
// is a Problem, never an error: the error is for a directory that cannot be
// set up at all.
func Prepare(ctx context.Context, dispatch Dispatch, options Options) (Report, error) {
	if dispatch.Directory == "" || dispatch.Mirrors == "" {
		return Report{}, nil
	}
	if !filepath.IsAbs(dispatch.Directory) || !filepath.IsAbs(dispatch.Mirrors) {
		return Report{}, fmt.Errorf("the working and mirrors directories must be absolute paths")
	}
	// The agent's user owns the directory and may have left links in it on
	// an earlier turn: every file operation stays inside it.
	root, err := os.OpenRoot(dispatch.Directory)
	if err != nil {
		return Report{}, fmt.Errorf("open the working directory: %w", err)
	}
	defer root.Close()
	if _, err := root.Lstat(StateFile); err == nil {
		return Report{}, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Report{}, fmt.Errorf("read the workspace state: %w", err)
	}
	if options.RunAs != nil {
		// The agent's user owns the directory it works in; the volume's
		// sub-path may have been created by someone else.
		if err := os.Lchown(dispatch.Directory, int(options.RunAs.UID), int(options.RunAs.GID)); err != nil {
			return Report{}, fmt.Errorf("hand the working directory to the agent's user: %w", err)
		}
	}
	repositories, err := Mirrors(dispatch.Mirrors)
	if err != nil {
		return Report{}, err
	}
	git := gitRunner{root: root, environment: options.Environment, runAs: options.RunAs}
	report := Report{SetUp: true}
	for _, repository := range repositories {
		if err := git.clone(ctx, dispatch, repository); err != nil {
			report.Problems = append(report.Problems, Problem{Repository: repository, Reason: "not cloned: " + err.Error()})
			continue
		}
		report.Cloned = append(report.Cloned, repository)
	}
	report.Fetched, report.Problems = git.fetchChanged(ctx, dispatch, report.Cloned, options, report.Problems)
	if options.Notes != "" {
		if err := writeOwned(root, options.Notes, []byte(notes(report.Cloned)), options.RunAs); err != nil {
			return report, fmt.Errorf("write the workspace notes: %w", err)
		}
	}
	recorded, err := json.Marshal(state{SetUp: time.Now().UTC(), Cloned: report.Cloned, Fetched: report.Fetched, Problems: report.Problems})
	if err != nil {
		return report, err
	}
	if err := root.MkdirAll(path.Dir(StateFile), 0o755); err != nil {
		return report, fmt.Errorf("record the workspace state: %w", err)
	}
	if err := root.WriteFile(StateFile, recorded, 0o644); err != nil {
		return report, fmt.Errorf("record the workspace state: %w", err)
	}
	return report, nil
}

// Mirrors lists the repositories, <owner>/<repository>, whose bare mirror
// <owner>/<repository>.git is under dir, in name order.
func Mirrors(dir string) ([]string, error) {
	owners, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list the mirrors: %w", err)
	}
	var repositories []string
	for _, owner := range owners {
		if !owner.IsDir() || strings.HasPrefix(owner.Name(), ".") {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, owner.Name()))
		if err != nil {
			return nil, fmt.Errorf("list the mirrors of %s: %w", owner.Name(), err)
		}
		for _, entry := range entries {
			name, isMirror := strings.CutSuffix(entry.Name(), ".git")
			if entry.IsDir() && isMirror && name != "" {
				repositories = append(repositories, owner.Name()+"/"+name)
			}
		}
	}
	return repositories, nil
}

// notes tells the agent how the directory is laid out and how to start work
// in a repository.
func notes(cloned []string) string {
	var b strings.Builder
	b.WriteString("# Workspace\n\n")
	b.WriteString("Each repository of this workspace is a git clone at `<owner>/<repository>` in this directory. ")
	b.WriteString("A clone borrows its objects from the workspace's read-only mirror and has no working tree yet: ")
	b.WriteString("check out the default branch where you work, with `git switch <default branch>` in the clone ")
	b.WriteString("(`git -C <owner>/<repository> remote show origin` names it). ")
	b.WriteString("`origin` is the repository on its provider: fetch, branch, commit and push there as usual.\n\n")
	if len(cloned) == 0 {
		b.WriteString("This workspace has no repositories.\n")
		return b.String()
	}
	b.WriteString("Repositories:\n\n")
	for _, repository := range cloned {
		b.WriteString("- `" + repository + "`\n")
	}
	return b.String()
}

// writeOwned writes a file under root the agent's user owns, readable by
// everyone.
func writeOwned(root *os.Root, name string, contents []byte, owner *Identity) error {
	if err := root.WriteFile(name, contents, 0o644); err != nil {
		return err
	}
	if owner != nil {
		return root.Lchown(name, int(owner.UID), int(owner.GID))
	}
	return nil
}

type gitRunner struct {
	// root is the working directory, which every file operation stays in.
	root        *os.Root
	environment []string
	runAs       *Identity
}

// clone makes the clone with alternates of repository's mirror, with no
// working tree and no local branch: the agent's first `git switch` of the
// default branch creates it from origin, after any fetch.
func (g gitRunner) clone(ctx context.Context, dispatch Dispatch, repository string) error {
	mirror := filepath.Join(dispatch.Mirrors, filepath.FromSlash(repository)+".git")
	clone := filepath.Join(dispatch.Directory, filepath.FromSlash(repository))
	if _, err := g.root.Lstat(repository + "/.git"); err == nil {
		// A setup that stopped before recording its state cloned it already.
		return nil
	}
	if err := g.mkdirOwned(path.Dir(repository)); err != nil {
		return err
	}
	// The mirror belongs to the volume's owner, not the agent's user: trust
	// it for this clone only. The clone reads its objects through alternates,
	// which never checks ownership.
	if _, err := g.run(ctx, "", "-c", "safe.directory="+mirror, "clone", "--quiet", "--shared", "--no-checkout",
		// A volume shared over the network: parallel checkout, the untracked
		// cache and the index settings for many files.
		"--config", "checkout.workers=0", "--config", "core.untrackedCache=true", "--config", "feature.manyFiles=true",
		mirror, clone); err != nil {
		return err
	}
	head, err := g.run(ctx, clone, "symbolic-ref", "--quiet", "HEAD")
	if err != nil {
		return err
	}
	if _, err := g.run(ctx, clone, "update-ref", "-d", head); err != nil {
		return err
	}
	origin, err := g.run(ctx, "", "-c", "safe.directory="+mirror, "-C", mirror, "config", "--get", "remote.origin.url")
	if err != nil || origin == "" {
		// A mirror without an origin stays the clone's origin.
		return nil
	}
	_, err = g.run(ctx, clone, "remote", "set-url", "origin", withoutUserinfo(origin))
	return err
}

// fetchChanged fetches every changed repository that was cloned from its
// origin, each under its own timeout and all of them under one budget.
func (g gitRunner) fetchChanged(ctx context.Context, dispatch Dispatch, cloned []string, options Options, problems []Problem) ([]string, []Problem) {
	timeout, budget := options.FetchTimeout, options.FetchBudget
	if timeout <= 0 {
		timeout = DefaultFetchTimeout
	}
	if budget <= 0 {
		budget = DefaultFetchBudget
	}
	budgetCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	var fetched []string
	for _, repository := range dispatch.Changed {
		if !slices.Contains(cloned, repository) {
			if !slices.ContainsFunc(problems, func(p Problem) bool { return p.Repository == repository }) {
				problems = append(problems, Problem{Repository: repository, Reason: "not fetched: the workspace has no mirror of it"})
			}
			continue
		}
		if budgetCtx.Err() != nil {
			problems = append(problems, Problem{Repository: repository, Reason: fmt.Sprintf("not fetched: the setup's fetch budget of %s ran out", budget)})
			continue
		}
		fetchCtx, cancelFetch := context.WithTimeout(budgetCtx, timeout)
		_, err := g.run(fetchCtx, filepath.Join(dispatch.Directory, filepath.FromSlash(repository)), "fetch", "--quiet", "--prune", "origin")
		timedOut := errors.Is(fetchCtx.Err(), context.DeadlineExceeded)
		cancelFetch()
		switch {
		case err == nil:
			fetched = append(fetched, repository)
		case timedOut:
			problems = append(problems, Problem{Repository: repository, Reason: fmt.Sprintf("the fetch from origin timed out after %s", timeout)})
		default:
			problems = append(problems, Problem{Repository: repository, Reason: "the origin refused the fetch: " + err.Error()})
		}
	}
	return fetched, problems
}

// mkdirOwned creates dir, a slash-separated path under the working
// directory, and its parents, owned by the agent's user.
func (g gitRunner) mkdirOwned(dir string) error {
	current := ""
	for part := range strings.SplitSeq(dir, "/") {
		current = path.Join(current, part)
		if err := g.root.Mkdir(current, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if g.runAs != nil {
			if err := g.root.Lchown(current, int(g.runAs.UID), int(g.runAs.GID)); err != nil {
				return err
			}
		}
	}
	return nil
}

// run runs git in dir and returns its trimmed standard output; an error
// carries the end of its standard error.
func (g gitRunner) run(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	// No prompt can be answered: a missing credential fails the command.
	command.Env = append(append([]string(nil), g.environment...), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	// A timeout ends git and every helper it started, such as its remote
	// transport, which would otherwise hold the output open. The group comes
	// first: it replaces the process attributes RunAs adds to.
	utils.ConfigureProcessGroup(command)
	if g.runAs != nil {
		utils.RunAs(command, g.runAs.UID, g.runAs.GID)
	}
	command.Cancel = func() error { return utils.KillProcessGroup(command.Process) }
	command.WaitDelay = 5 * time.Second
	stderr := utils.NewBoundedBuffer(16 << 10)
	command.Stderr = stderr
	output, err := command.Output()
	if err != nil {
		return "", errors.New(reason(stderr.String(), err))
	}
	return strings.TrimSpace(string(output)), nil
}

// reason is the last line of git's error output, bounded, or the exit error
// without one.
func reason(stderr string, err error) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return err.Error()
	}
	if len(last) > maxReasonBytes {
		last = last[:maxReasonBytes]
	}
	return last
}

// withoutUserinfo drops a credential a mirror's origin URL may carry, so the
// clone keeps none.
func withoutUserinfo(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.User == nil || parsed.Scheme == "" {
		return origin
	}
	parsed.User = nil
	return parsed.String()
}

// validRepository reports whether repository is <owner>/<repository>.
func validRepository(repository string) bool {
	owner, name, ok := strings.Cut(repository, "/")
	return ok && owner != "" && name != "" && !strings.Contains(name, "/") && path.Clean(repository) == repository &&
		owner != ".." && name != ".." && owner != "." && name != "."
}
