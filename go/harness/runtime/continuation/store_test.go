package continuation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func acceptOnly(want string) Validator {
	return func(id string) error {
		if id != want {
			return errors.New("invalid ID")
		}
		return nil
	}
}

func acceptAny(string) error { return nil }

func TestStorePreservesStateFormatAndUsesRuntimeValidator(t *testing.T) {
	directory := t.TempDir()
	validate := acceptOnly("opaque-thread")
	store, err := New(directory, "codex", validate)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Bind("opaque-thread"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(directory, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"version\": 2,\n  \"runtime\": \"codex\",\n  \"session_id\": \"opaque-thread\"\n}"
	if string(contents) != want {
		t.Fatalf("state = %s, want %s", contents, want)
	}
	reloaded, err := New(directory, "codex", validate)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok, err := reloaded.Load(); err != nil || !ok || id != "opaque-thread" {
		t.Fatalf("Load() = %q, %t, %v", id, ok, err)
	}
	if err := reloaded.Bind("different"); err == nil {
		t.Fatal("Bind() accepted an invalid continuation")
	}
}

// A process restored from the golden snapshot holds a store created before any
// turn ran; the durable directory it is restored with carries the state a later
// turn bound. Load must return that persisted identity, not the stale cache.
func TestLoadSeesContinuationBoundThroughAnotherStore(t *testing.T) {
	directory := t.TempDir()
	golden, err := New(directory, "claude", acceptAny)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok, err := golden.Load(); err != nil || ok || id != "" {
		t.Fatalf("Load() before any turn = %q, %t, %v", id, ok, err)
	}
	firstTurn, err := New(directory, "claude", acceptAny)
	if err != nil {
		t.Fatal(err)
	}
	if err := firstTurn.Bind("session-1"); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := golden.Load(); err != nil || !ok || id != "session-1" {
		t.Fatalf("Load() after another store bound = %q, %t, %v", id, ok, err)
	}
	if err := golden.Bind("session-1"); err != nil {
		t.Fatalf("Bind() of the persisted identity = %v", err)
	}
	if err := golden.Bind("session-2"); err == nil {
		t.Fatal("Bind() rebound an actor that is persisted as bound to another continuation")
	}
}

func TestLoadFollowsExternalWritesAndRemoval(t *testing.T) {
	directory := t.TempDir()
	store, err := New(directory, "claude", acceptAny)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "state.json")
	external := []byte("{\n  \"version\": 2,\n  \"runtime\": \"claude\",\n  \"session_id\": \"external\"\n}")
	if err := os.WriteFile(path, external, 0o600); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := store.Load(); err != nil || !ok || id != "external" {
		t.Fatalf("Load() after external write = %q, %t, %v", id, ok, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := store.Load(); err != nil || ok || id != "" {
		t.Fatalf("Load() after external removal = %q, %t, %v", id, ok, err)
	}
	if err := os.WriteFile(path, []byte("{\"version\": 1, \"runtime\": \"claude\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(); err == nil {
		t.Fatal("Load() accepted a state file of another version")
	}
	if err := os.WriteFile(path, []byte("{\"version\": 2, \"runtime\": \"claude\", \"session_id\": \"bad\"}"), 0o600); err != nil {
		t.Fatal(err)
	}
	rejecting, err := New(directory, "claude", acceptOnly("good"))
	if err == nil {
		t.Fatal("New() accepted a persisted continuation the runtime validator rejects")
	}
	if rejecting != nil {
		t.Fatal("New() returned a store together with an error")
	}
}
