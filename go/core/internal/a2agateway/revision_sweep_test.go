package a2agateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

type revisionSweepTestStore struct {
	mu        sync.Mutex
	instances []*apiv1alpha1.AgentInstance
	listErr   error
	lists     int
}

func (s *revisionSweepTestStore) ListAgentInstancesOnSupersededRevisions(_ context.Context, afterID string, limit int) ([]*apiv1alpha1.AgentInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	if s.listErr != nil {
		return nil, s.listErr
	}
	var result []*apiv1alpha1.AgentInstance
	for _, instance := range s.instances {
		if instance.GetPreparedRevision() == "superseded" && instance.GetId() > afterID && len(result) < limit {
			result = append(result, proto.CloneOf(instance))
		}
	}
	return result, nil
}

// revisionSweepTestWorkflow moves every instance it is given onto "current",
// or fails the ones in failing.
type revisionSweepTestWorkflow struct {
	mu      sync.Mutex
	store   *revisionSweepTestStore
	failing map[string]error
	calls   []string
}

func (w *revisionSweepTestWorkflow) RepointQuiesced(_ context.Context, instance *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, instance.GetId())
	if err := w.failing[instance.GetId()]; err != nil {
		return nil, err
	}
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	for _, stored := range w.store.instances {
		if stored.GetId() == instance.GetId() {
			stored.PreparedRevision = "current"
			return proto.CloneOf(stored), nil
		}
	}
	return nil, database.ErrNotFound
}

func revisionSweepTestInstances(n int) []*apiv1alpha1.AgentInstance {
	instances := make([]*apiv1alpha1.AgentInstance, 0, n)
	for i := range n {
		instances = append(instances, &apiv1alpha1.AgentInstance{
			Id: fmt.Sprintf("instance-%03d", i), PreparedRevision: "superseded",
			State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		})
	}
	return instances
}

func TestRevisionSweepMovesEveryInstancePageByPage(t *testing.T) {
	store := &revisionSweepTestStore{instances: revisionSweepTestInstances(revisionSweepPage + 1)}
	workflow := &revisionSweepTestWorkflow{store: store}
	sweep := newRevisionSweep(store, workflow, time.Minute, &memoryRuntimeCoordinator{})

	sweep.sweep(t.Context())
	require.Len(t, workflow.calls, revisionSweepPage+1, "every instance is moved once")
	require.Equal(t, 2, store.lists, "a full page is followed by the next, the short one ends the sweep")
	remaining, err := store.ListAgentInstancesOnSupersededRevisions(t.Context(), "", revisionSweepPage)
	require.NoError(t, err)
	require.Empty(t, remaining)
}

func TestRevisionSweepLeavesATurnInFlightAndAFailedMoveToTheNextSweep(t *testing.T) {
	store := &revisionSweepTestStore{instances: revisionSweepTestInstances(3)}
	workflow := &revisionSweepTestWorkflow{store: store, failing: map[string]error{
		"instance-001": errors.New("ate-api unavailable"),
		"instance-002": fmt.Errorf("a suspend took the instance: %w", database.ErrConflict),
	}}
	coordinator := &memoryRuntimeCoordinator{}
	sweep := newRevisionSweep(store, workflow, time.Minute, coordinator)

	release := coordinator.RuntimeCall("instance-000")
	sweep.sweep(t.Context())
	require.Equal(t, []string{"instance-001", "instance-002"}, workflow.calls, "an instance whose turn is in flight is skipped")
	release()

	workflow.failing = nil
	sweep.sweep(t.Context())
	require.Equal(t, []string{"instance-001", "instance-002", "instance-000", "instance-001", "instance-002"}, workflow.calls)
	remaining, err := store.ListAgentInstancesOnSupersededRevisions(t.Context(), "", revisionSweepPage)
	require.NoError(t, err)
	require.Empty(t, remaining, "the next sweep moves what the last one could not")
}

func TestRevisionSweepRunsOnItsInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &revisionSweepTestStore{}
		workflow := &revisionSweepTestWorkflow{store: store}
		sweep := newRevisionSweep(store, workflow, 5*time.Minute, &memoryRuntimeCoordinator{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- sweep.Start(ctx) }()
		synctest.Wait()
		require.Equal(t, 1, store.lists, "a sweep runs at start")

		store.mu.Lock()
		store.instances = revisionSweepTestInstances(1)
		store.mu.Unlock()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		require.Equal(t, 2, store.lists)
		require.Equal(t, []string{"instance-000"}, workflow.calls, "an instance that appeared since is moved on the next interval")

		cancel()
		require.NoError(t, <-done)
	})
}
