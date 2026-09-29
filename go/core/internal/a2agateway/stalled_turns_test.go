package a2agateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/stretchr/testify/require"
)

const stalledTurnsTestTimeout = time.Hour

type stalledTurnsTestStore struct {
	mu           sync.Mutex
	working      []database.StalledAgentInstanceTask
	listErr      error
	interruptErr map[string]error
	interrupted  []string
}

func (s *stalledTurnsTestStore) ListAgentInstanceTasksWorkingBefore(_ context.Context, cutoff time.Time, limit int) ([]database.StalledAgentInstanceTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	var result []database.StalledAgentInstanceTask
	for _, candidate := range s.working {
		if candidate.LastEventAt.Before(cutoff) && len(result) < limit {
			result = append(result, candidate)
		}
	}
	return result, nil
}

func (s *stalledTurnsTestStore) InterruptActiveAgentInstanceTask(_ context.Context, instanceID, taskID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.interruptErr[instanceID]; err != nil {
		return false, err
	}
	for i, candidate := range s.working {
		if candidate.InstanceID == instanceID && candidate.TaskID == taskID {
			s.working = append(s.working[:i:i], s.working[i+1:]...)
			s.interrupted = append(s.interrupted, instanceID+"/"+taskID)
			return true, nil
		}
	}
	return false, nil
}

func (s *stalledTurnsTestStore) interruptedTasks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string{}, s.interrupted...)
}

func stalledTurnsTestCandidate(instanceID, taskID string, stalledFor time.Duration) database.StalledAgentInstanceTask {
	return database.StalledAgentInstanceTask{InstanceID: instanceID, TaskID: taskID, LastEventAt: time.Now().Add(-stalledFor)}
}

func stalledTurnsTestSweep(store *stalledTurnsTestStore) (*StalledTurns, *Gateway) {
	gateway := &Gateway{coordinator: &memoryRuntimeCoordinator{}}
	return newStalledTurns(store, gateway, stalledTurnsTestTimeout), gateway
}

func TestStalledTurnsFailsAWorkingTaskWithoutEventsBeyondTheTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		store := &stalledTurnsTestStore{working: []database.StalledAgentInstanceTask{
			stalledTurnsTestCandidate("lost", "task-1", 13*24*time.Hour),
			stalledTurnsTestCandidate("quiet", "task-2", 50*time.Minute),
		}}
		sweep, _ := stalledTurnsTestSweep(store)
		require.True(t, sweep.NeedLeaderElection())
		done := make(chan error, 1)
		go func() { done <- sweep.Start(ctx) }()
		synctest.Wait()
		require.Equal(t, []string{"lost/task-1"}, store.interruptedTasks(), "startup sweeps at once; a turn quiet for less than the timeout is left alone")

		time.Sleep(10*time.Minute + sweep.interval())
		synctest.Wait()
		require.Equal(t, []string{"lost/task-1", "quiet/task-2"}, store.interruptedTasks(), "the turn fails once it has been quiet beyond the timeout")
		cancel()
		require.NoError(t, <-done)
	})
}

func TestStalledTurnsLeavesATurnARunFollows(t *testing.T) {
	store := &stalledTurnsTestStore{working: []database.StalledAgentInstanceTask{stalledTurnsTestCandidate(gatewayTestID, "task-1", 2*time.Hour)}}
	sweep, gateway := stalledTurnsTestSweep(store)
	gateway.runs.Store(taskRunKey(gatewayTestID, "task-1"), &taskRun{})
	sweep.sweep(t.Context())
	require.Empty(t, store.interruptedTasks(), "a run records the turn's next event or its end")

	gateway.runs.Delete(taskRunKey(gatewayTestID, "task-1"))
	sweep.sweep(t.Context())
	require.Equal(t, []string{gatewayTestID + "/task-1"}, store.interruptedTasks())
}

func TestStalledTurnsLeavesATurnInFlightAndRetries(t *testing.T) {
	store := &stalledTurnsTestStore{working: []database.StalledAgentInstanceTask{stalledTurnsTestCandidate(gatewayTestID, "task-1", 2*time.Hour)}}
	sweep, gateway := stalledTurnsTestSweep(store)
	release := gateway.coordinator.RuntimeCall(gatewayTestID)
	sweep.sweep(t.Context())
	require.Empty(t, store.interruptedTasks(), "a dispatch in flight wins")
	release()
	sweep.sweep(t.Context())
	require.Equal(t, []string{gatewayTestID + "/task-1"}, store.interruptedTasks(), "the next sweep fails the task")
}

func TestStalledTurnsToleratesFailuresPerCandidate(t *testing.T) {
	store := &stalledTurnsTestStore{
		working: []database.StalledAgentInstanceTask{
			stalledTurnsTestCandidate("first", "task-1", 2*time.Hour),
			stalledTurnsTestCandidate("second", "task-2", 2*time.Hour),
		},
		interruptErr: map[string]error{"first": errors.New("database unavailable")},
	}
	sweep, _ := stalledTurnsTestSweep(store)
	sweep.sweep(t.Context())
	require.Equal(t, []string{"second/task-2"}, store.interruptedTasks(), "a failed candidate does not block the next")

	store = &stalledTurnsTestStore{listErr: errors.New("database unavailable")}
	sweep, _ = stalledTurnsTestSweep(store)
	sweep.sweep(t.Context())
	require.Empty(t, store.interruptedTasks())
}

func TestStalledTurnsInterval(t *testing.T) {
	for timeout, want := range map[time.Duration]time.Duration{
		time.Hour:        time.Minute,
		time.Minute:      30 * time.Second,
		time.Second:      time.Second,
		10 * time.Minute: time.Minute,
	} {
		require.Equal(t, want, (&StalledTurns{timeout: timeout}).interval(), "timeout %s", timeout)
	}
}

func TestNewStalledTurnsNeedsTheGatewayHandler(t *testing.T) {
	handler := New(&gatewayTestStore{instance: gatewayTestInstance()}, &gatewayTestAuthorizer{}, &gatewayTestDialer{}, &gatewayTestWorkflow{}, gatewayTestURL)
	sweep, err := NewStalledTurns(&stalledTurnsTestStore{}, handler, stalledTurnsTestTimeout)
	require.NoError(t, err)
	require.Same(t, handler.(*a2asrv.InterceptedHandler).Handler, sweep.gateway)

	_, err = NewStalledTurns(&stalledTurnsTestStore{}, &a2asrv.InterceptedHandler{}, stalledTurnsTestTimeout)
	require.Error(t, err)
}
