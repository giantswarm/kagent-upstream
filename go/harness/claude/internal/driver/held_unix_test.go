//go:build unix

package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kagent-dev/kagent/go/harness/runtime"
)

// fakeLifetime is how many 20 ms loops a fake Claude and its command run
// before they exit by themselves: about 30 seconds.
const fakeLifetime = "1500"

// heldClaude is a Claude that reports its result after listing tasks in
// the background and then stays alive, as Claude Code does for a command
// that outlived its Bash timeout: the command ignores the interrupt and
// writes to activity until it is killed. Both give up after fakeLifetime
// loops on their own, so a driver that fails to stop them, or a test binary
// that times out, leaves nothing running for long.
func heldClaude(t *testing.T, tasks string) (dir, executable, activity string) {
	t.Helper()
	dir = t.TempDir()
	executable = filepath.Join(dir, "claude")
	activity = filepath.Join(dir, "activity")
	script := `#!/bin/sh
trap 'exit 0' INT
sh -c 'trap "" INT TERM; i=0; while [ $i -lt ` + fakeLifetime + ` ]; do i=$((i+1)); printf x >> "$ACTIVITY"; sleep 0.02; done' &
printf '%s\n' '{"type":"system","subtype":"init","session_id":"11111111-1111-4111-8111-111111111111"}'
printf '%s\n' '{"type":"system","subtype":"background_tasks_changed","tasks":` + tasks + `}'
printf '%s\n' '{"type":"result","subtype":"success","is_error":true,"result":"There is an issue with the selected model","session_id":"11111111-1111-4111-8111-111111111111","num_turns":2}'
cat >/dev/null
i=0; while [ $i -lt ` + fakeLifetime + ` ]; do i=$((i+1)); sleep 0.02; done
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, executable, activity
}

// A Claude that reports its result while a command it moved to the
// background still runs ends its turn at once with that result, the command
// stopped and named to the person, instead of leaving the turn working.
func TestProcessDriverEndsATurnHeldByABackgroundCommand(t *testing.T) {
	dir, executable, activity := heldClaude(t, `[{"task_id":"b1","description":"git fetch origin main\nreport fetch"}]`)
	d := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: dir, Environment: []string{"ACTIVITY=" + activity},
		MaxEventBytes: 4096, MaxStderrBytes: 1024, InterruptGrace: 50 * time.Millisecond,
	})
	sink := &recordingSink{}
	started := time.Now()
	outcome, err := d.Run(t.Context(), runtime.Turn{Prompt: "hello"}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v, want Claude's result", err)
	}
	if elapsed := time.Since(started); elapsed >= resultExitGrace {
		t.Fatalf("the held turn took %s, want it ended at the result", elapsed)
	}
	if outcome.Failure == nil || outcome.Failure.Message != "There is an issue with the selected model" || outcome.Usage == nil || outcome.Usage.NumTurns != 2 {
		t.Fatalf("Run() = %#v, want Claude's failed result with its usage", outcome)
	}
	if len(sink.notices) != 1 || !strings.Contains(sink.notices[0].Text, "`git fetch origin main`") {
		t.Fatalf("notices = %#v, want the stopped command named by its first line", sink.notices)
	}
	requireStopped(t, activity)
}

// A Claude that lists nothing in the background and still does not exit
// after its result ends its turn with that result once resultExitGrace has
// passed.
func TestProcessDriverEndsATurnWhoseClaudeDoesNotExitAfterItsResult(t *testing.T) {
	grace := resultExitGrace
	resultExitGrace = 100 * time.Millisecond
	t.Cleanup(func() { resultExitGrace = grace })
	dir, executable, activity := heldClaude(t, `[]`)
	d := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: dir, Environment: []string{"ACTIVITY=" + activity},
		MaxEventBytes: 4096, MaxStderrBytes: 1024, InterruptGrace: 50 * time.Millisecond,
	})
	sink := &recordingSink{}
	outcome, err := d.Run(t.Context(), runtime.Turn{Prompt: "hello"}, sink)
	if err != nil || outcome.Failure == nil {
		t.Fatalf("Run() = %#v, %v; want Claude's failed result", outcome, err)
	}
	if len(sink.notices) != 0 {
		t.Fatalf("notices = %#v, want none when nothing was listed in the background", sink.notices)
	}
	requireStopped(t, activity)
}

// A Claude that reports no session within sessionStartBound fails its turn
// with the stall named, instead of leaving the turn working.
func TestProcessDriverFailsATurnWhoseClaudeStallsBeforeItsSession(t *testing.T) {
	bound := sessionStartBound
	sessionStartBound = 100 * time.Millisecond
	t.Cleanup(func() { sessionStartBound = bound })
	dir := t.TempDir()
	executable := filepath.Join(dir, "claude")
	activity := filepath.Join(dir, "activity")
	script := `#!/bin/sh
echo 'waiting on something' >&2
i=0; while [ $i -lt ` + fakeLifetime + ` ]; do i=$((i+1)); printf x >> "$ACTIVITY"; sleep 0.02; done
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	d := NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: dir, Environment: []string{"ACTIVITY=" + activity},
		MaxEventBytes: 4096, MaxStderrBytes: 1024, InterruptGrace: 50 * time.Millisecond,
	})
	_, err := d.Run(t.Context(), runtime.Turn{Prompt: "hello"}, &recordingSink{})
	if err == nil || !strings.Contains(err.Error(), "Claude stalled: it reported no session within 100ms") || !strings.Contains(err.Error(), "waiting on something") {
		t.Fatalf("Run() error = %v, want the stall named with Claude's stderr", err)
	}
	requireStopped(t, activity)
}

func TestHeldBackgroundNotice(t *testing.T) {
	if got := heldBackgroundNotice(nil); got != "" {
		t.Fatalf("heldBackgroundNotice(nil) = %q, want none", got)
	}
	long := strings.Repeat("é", heldBackgroundNoticeLimit+5)
	got := heldBackgroundNotice([]string{"  npm run dev\n", "sleep 600\necho done", long})
	want := "Stopped what was still running in the background when the turn ended: `npm run dev`, `sleep 600`, `" +
		strings.Repeat("é", heldBackgroundNoticeLimit) + "…`"
	if got != want {
		t.Fatalf("heldBackgroundNotice() = %q, want %q", got, want)
	}
}

// requireStopped fails unless the descendant writing activity has stopped.
func requireStopped(t *testing.T, activity string) {
	t.Helper()
	before, err := os.Stat(activity)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	after, err := os.Stat(activity)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("the background command kept running after the turn: activity grew from %d to %d bytes", before.Size(), after.Size())
	}
}
