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
printf '%s\n' '{"type":"result","subtype":"success","is_error":true,"result":"There is an issue with the selected model","session_id":"11111111-1111-4111-8111-111111111111"}'
cat >/dev/null
i=0; while [ $i -lt ` + fakeLifetime + ` ]; do i=$((i+1)); sleep 0.02; done
`
	if err := os.WriteFile(executable, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, executable, activity
}

func heldDriver(dir, executable, activity string) *ProcessDriver {
	return NewProcessDriver(ProcessConfig{
		Executable: executable, Workspace: dir, Environment: []string{"ACTIVITY=" + activity},
		MaxEventBytes: 4096, MaxStderrBytes: 1024, InterruptGrace: 50 * time.Millisecond,
	})
}

// A Claude that reports its result while a command it moved to the
// background still runs ends its turn at once with that result, the command
// stopped, instead of leaving the turn working.
func TestProcessDriverEndsATurnHeldByABackgroundCommand(t *testing.T) {
	dir, executable, activity := heldClaude(t, `[{"task_id":"b1","description":"git fetch origin main"}]`)
	started := time.Now()
	outcome, err := heldDriver(dir, executable, activity).Run(t.Context(), runtime.Turn{Prompt: "hello"}, &recordingSink{})
	if err != nil {
		t.Fatalf("Run() error = %v, want Claude's result", err)
	}
	if elapsed := time.Since(started); elapsed >= resultExitGrace {
		t.Fatalf("the held turn took %s, want it ended at the result", elapsed)
	}
	if outcome.Failure == nil || outcome.Failure.Message != "There is an issue with the selected model" {
		t.Fatalf("Run() = %#v, want Claude's failed result", outcome)
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
	outcome, err := heldDriver(dir, executable, activity).Run(t.Context(), runtime.Turn{Prompt: "hello"}, &recordingSink{})
	if err != nil || outcome.Failure == nil {
		t.Fatalf("Run() = %#v, %v; want Claude's failed result", outcome, err)
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
	_, err := heldDriver(dir, executable, activity).Run(t.Context(), runtime.Turn{Prompt: "hello"}, &recordingSink{})
	if err == nil || !strings.Contains(err.Error(), "Claude stalled: it reported no session within 100ms") || !strings.Contains(err.Error(), "waiting on something") {
		t.Fatalf("Run() error = %v, want the stall named with Claude's stderr", err)
	}
	requireStopped(t, activity)
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
