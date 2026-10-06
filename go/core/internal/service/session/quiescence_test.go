package session

import (
	"context"
	"crypto/sha256"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/uuid"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIdleLifecycleDoesNotOwnTaskPublication(t *testing.T) {
	for _, test := range []struct {
		name           string
		mutationFails  bool
		finishFailures int32
	}{
		{name: "snapshot succeeds"},
		{name: "lost suspend response settles from the Actor", mutationFails: true},
		{name: "snapshot reference survives database retries", finishFailures: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, session := lifecycleFixture(t)
			base := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{}}
			session, err := NewActorWorkflow(store, base).Create(t.Context(), session)
			require.NoError(t, err)
			message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
			message.ContextID = session.ContextId
			task := a2a.NewSubmittedTask(message, message)
			task.Status.State = a2a.TaskStateCompleted
			hash := sha256.Sum256([]byte("completed"))
			version, err := store.CreateRuntimeTask(t.Context(), session.Id, hash[:], task, "")
			require.NoError(t, err)
			require.NoError(t, store.SettleSessionTask(t.Context(), session.Id, string(task.ID), version))

			entered, release := make(chan struct{}), make(chan struct{})
			var enter sync.Once
			actors := &retryTestActors{lifecycleTestActors: base, beforeRead: func(ctx context.Context) {
				enter.Do(func() { close(entered) })
				select {
				case <-release:
				case <-ctx.Done():
				}
			}}
			if test.mutationFails {
				actors.mutationErr = status.Error(codes.Unavailable, "lost suspend response")
			}
			// A new lifecycle worker discovers durable idle work without any
			// notification or participation from the task persistence service.
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			writes := &quiescenceRetryStore{lifecycleTestStore: store, failures: test.finishFailures}
			go func() { done <- NewActorWorkflow(writes, actors).Start(ctx) }()
			t.Cleanup(func() { cancel(); require.NoError(t, <-done) })
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("idle work was not discovered")
			}
			visible, err := store.GetSettledSessionTask(t.Context(), session.Id, string(task.ID), nil)
			require.NoError(t, err)
			require.Equal(t, a2a.TaskStateCompleted, visible.Status.State)
			next := a2a.NewSubmittedTask(message, message)
			_, err = store.CreateRuntimeTask(t.Context(), session.Id, hash[:], next, "")
			require.ErrorIs(t, err, database.ErrFailedPrecondition)
			checkpoint := &apiv1alpha1.Checkpoint{Id: uuid.NewString(), SessionId: session.Id, HeadTaskId: string(task.ID)}
			_, _, err = store.ReserveSessionCheckpoint(t.Context(), checkpoint, "alice", "checkpoint")
			require.ErrorIs(t, err, database.ErrFailedPrecondition)
			close(release)

			// A suspend whose response was lost is recorded from the Actor's
			// snapshot, never by suspending it again.
			require.Eventually(t, func() bool {
				_, snapshot, err := store.ReserveSessionCheckpoint(t.Context(), checkpoint, "alice", "checkpoint")
				return err == nil && snapshot.URI == "s3://snapshots/snapshot-1"
			}, 5*time.Second, 10*time.Millisecond)
			require.EqualValues(t, 1, actors.mutations.Load(), "database retries must not suspend the actor again")
			require.Equal(t, test.finishFailures+1, writes.attempts.Load())
			_, err = store.ClaimSessionQuiescence(t.Context(), 0, nil)
			require.ErrorIs(t, err, database.ErrNotFound)
		})
	}
}

// A Pause or Quiesce that fails without an answer leaves the claim held while
// the Actor is still in transition, and the Actor's state then settles it: a
// boundary that landed is recorded, any other state releases the claim without
// a snapshot. Either way the next turn is admitted.
func TestFailedBoundarySettlesFromTheActor(t *testing.T) {
	suspend := func(a *lifecycleTestActors, space, name string) {
		_, _ = a.SuspendActor(context.Background(), space, name)
	}
	pause := func(a *lifecycleTestActors, space, name string) {
		_, _ = a.PauseActor(context.Background(), space, name)
	}
	become := func(state ateapipb.ActorState) func(*lifecycleTestActors, string, string) {
		return func(a *lifecycleTestActors, space, name string) { a.setState(space, name, state) }
	}
	gone := func(a *lifecycleTestActors, space, name string) {
		_ = a.DeleteActor(context.Background(), space, name, true)
	}
	for _, test := range []struct {
		name     string
		waiting  bool
		settle   func(*lifecycleTestActors, string, string)
		snapshot bool
	}{
		{name: "suspend lands", settle: suspend, snapshot: true},
		{name: "suspend did not happen", settle: become(ateapipb.ActorState_ACTOR_STATE_RUNNING)},
		{name: "runtime crashed", settle: become(ateapipb.ActorState_ACTOR_STATE_CRASHED)},
		{name: "actor gone", settle: gone},
		{name: "pause lands", waiting: true, settle: pause},
		{name: "pause did not happen", waiting: true, settle: become(ateapipb.ActorState_ACTOR_STATE_RUNNING)},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, session := lifecycleFixture(t)
			base := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{}}
			session, err := NewActorWorkflow(store, base).Create(t.Context(), session)
			require.NoError(t, err)
			message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
			message.ContextID = session.ContextId
			task := a2a.NewSubmittedTask(message, message)
			task.Status.State = a2a.TaskStateCompleted
			pending := ateapipb.ActorState_ACTOR_STATE_SUSPENDING
			if test.waiting {
				task.Status.State = a2a.TaskStateInputRequired
				pending = ateapipb.ActorState_ACTOR_STATE_PAUSING
			}
			hash := sha256.Sum256([]byte("turn"))
			version, err := store.CreateRuntimeTask(t.Context(), session.Id, hash[:], task, "")
			require.NoError(t, err)
			require.NoError(t, store.SettleSessionTask(t.Context(), session.Id, string(task.ID), version))

			actors := &unsettledActors{lifecycleTestActors: base, pending: pending, settle: test.settle, release: make(chan struct{}), reads: make(chan struct{}, 1)}
			workflow := NewActorWorkflow(store, actors)
			workflow.settleDelay = time.Millisecond
			work, err := store.ClaimSessionQuiescence(t.Context(), 0, nil)
			require.NoError(t, err)
			workflow.quiesceIdleSession(t.Context(), work)
			for range 2 {
				select {
				case <-actors.reads:
				case <-time.After(5 * time.Second):
					t.Fatal("the unsettled Actor was not read again")
				}
			}
			require.ErrorIs(t, store.ReserveSessionDispatch(t.Context(), session.Id, uuid.New(), "held"), database.ErrDispatchBusy, "an Actor in transition keeps the claim")

			close(actors.release)
			workflow.settling.Wait()
			require.EqualValues(t, 1, actors.mutations.Load(), "settling never repeats the runtime request")
			_, err = store.ClaimSessionQuiescence(t.Context(), 0, nil)
			require.ErrorIs(t, err, database.ErrNotFound, "the settled boundary is not claimed again")
			if test.snapshot {
				checkpoint := &apiv1alpha1.Checkpoint{Id: uuid.NewString(), SessionId: session.Id, HeadTaskId: string(task.ID)}
				_, snapshot, err := store.ReserveSessionCheckpoint(t.Context(), checkpoint, "alice", "checkpoint")
				require.NoError(t, err)
				require.Equal(t, "s3://snapshots/snapshot-1", snapshot.URI)
				return
			}
			require.NoError(t, store.ReserveSessionDispatch(t.Context(), session.Id, uuid.New(), "next"), "the settled claim admits the next turn")
		})
	}
}

// unsettledActors fails the boundary request after Substrate took it: the Actor
// reports pending until release is closed, and then what settle made of it.
type unsettledActors struct {
	*lifecycleTestActors
	pending   ateapipb.ActorState
	settle    func(*lifecycleTestActors, string, string)
	release   chan struct{}
	reads     chan struct{}
	failed    atomic.Bool
	settled   sync.Once
	mutations atomic.Int32
}

func (a *unsettledActors) GetActor(ctx context.Context, space, name string) (*ateapipb.Actor, error) {
	if a.failed.Load() {
		select {
		case <-a.release:
			a.settled.Do(func() { a.settle(a.lifecycleTestActors, space, name) })
		default:
			select {
			case a.reads <- struct{}{}:
			default:
			}
		}
	}
	return a.lifecycleTestActors.GetActor(ctx, space, name)
}

func (a *unsettledActors) SuspendActor(_ context.Context, space, name string) (*ateapipb.Actor, error) {
	return nil, a.fail(space, name)
}

func (a *unsettledActors) PauseActor(_ context.Context, space, name string) (*ateapipb.Actor, error) {
	return nil, a.fail(space, name)
}

func (a *unsettledActors) fail(space, name string) error {
	a.mutations.Add(1)
	a.setState(space, name, a.pending)
	a.failed.Store(true)
	return status.Error(codes.DeadlineExceeded, "sandbox teardown outlived the request")
}

func (a *lifecycleTestActors) setState(space, name string, state ateapipb.ActorState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actors[actorKey(space, name)].Status.State = state
}

type quiescenceRetryStore struct {
	*lifecycleTestStore
	failures int32
	attempts atomic.Int32
}

func (s *quiescenceRetryStore) FinishSessionQuiescence(ctx context.Context, work *database.SessionQuiescence, snapshot *database.SessionTaskSnapshot) error {
	if s.attempts.Add(1) <= s.failures {
		return status.Error(codes.Unavailable, "database unavailable")
	}
	return s.Client.FinishSessionQuiescence(ctx, work, snapshot)
}
