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

type deletedAgentSweepTestStore struct {
	mu        sync.Mutex
	instances []*apiv1alpha1.AgentInstance
	listErr   error
	lists     int
}

func (s *deletedAgentSweepTestStore) ListAgentInstancesOfDeletedAgents(_ context.Context, afterID string, limit int) ([]*apiv1alpha1.AgentInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	if s.listErr != nil {
		return nil, s.listErr
	}
	var result []*apiv1alpha1.AgentInstance
	for _, instance := range s.instances {
		if instance.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_DELETED &&
			instance.GetId() > afterID && len(result) < limit {
			result = append(result, proto.CloneOf(instance))
		}
	}
	return result, nil
}

// deletedAgentSweepTestWorkflow deletes every instance it is given, or fails the
// ones in failing.
type deletedAgentSweepTestWorkflow struct {
	mu      sync.Mutex
	store   *deletedAgentSweepTestStore
	failing map[string]error
	calls   []string
}

func (w *deletedAgentSweepTestWorkflow) Delete(_ context.Context, instance *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error) {
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
			stored.State = apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_DELETED
			return proto.CloneOf(stored), nil
		}
	}
	return nil, database.ErrNotFound
}

func deletedAgentSweepTestInstances(n int) []*apiv1alpha1.AgentInstance {
	instances := make([]*apiv1alpha1.AgentInstance, 0, n)
	for i := range n {
		instances = append(instances, &apiv1alpha1.AgentInstance{
			Id: fmt.Sprintf("instance-%03d", i), PreparedRevision: "revision-1",
			State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		})
	}
	return instances
}

func TestDeletedAgentSweepDeletesEveryInstancePageByPage(t *testing.T) {
	store := &deletedAgentSweepTestStore{instances: deletedAgentSweepTestInstances(deletedAgentSweepPage + 1)}
	workflow := &deletedAgentSweepTestWorkflow{store: store}
	sweep := newDeletedAgentSweep(store, workflow, time.Minute, &memoryRuntimeCoordinator{})

	sweep.sweep(t.Context())
	require.Len(t, workflow.calls, deletedAgentSweepPage+1, "every instance is deleted once")
	require.Equal(t, 2, store.lists, "a full page is followed by the next, the short one ends the sweep")
	remaining, err := store.ListAgentInstancesOfDeletedAgents(t.Context(), "", deletedAgentSweepPage)
	require.NoError(t, err)
	require.Empty(t, remaining)
}

func TestDeletedAgentSweepLeavesATurnInFlightAndAFailedDeleteToTheNextSweep(t *testing.T) {
	store := &deletedAgentSweepTestStore{instances: deletedAgentSweepTestInstances(3)}
	workflow := &deletedAgentSweepTestWorkflow{store: store, failing: map[string]error{
		"instance-001": errors.New("ate-api unavailable"),
		"instance-002": fmt.Errorf("the turn is not settled: %w", database.ErrFailedPrecondition),
	}}
	coordinator := &memoryRuntimeCoordinator{}
	sweep := newDeletedAgentSweep(store, workflow, time.Minute, coordinator)

	release := coordinator.RuntimeCall("instance-000")
	sweep.sweep(t.Context())
	require.Equal(t, []string{"instance-001", "instance-002"}, workflow.calls, "an instance whose turn is in flight is skipped")
	release()

	workflow.failing = nil
	sweep.sweep(t.Context())
	require.Equal(t, []string{"instance-001", "instance-002", "instance-000", "instance-001", "instance-002"}, workflow.calls)
	remaining, err := store.ListAgentInstancesOfDeletedAgents(t.Context(), "", deletedAgentSweepPage)
	require.NoError(t, err)
	require.Empty(t, remaining, "the next sweep deletes what the last one could not")
}

func TestDeletedAgentSweepRunsOnItsInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &deletedAgentSweepTestStore{}
		workflow := &deletedAgentSweepTestWorkflow{store: store}
		sweep := newDeletedAgentSweep(store, workflow, 5*time.Minute, &memoryRuntimeCoordinator{})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- sweep.Start(ctx) }()
		synctest.Wait()
		require.Equal(t, 1, store.lists, "a sweep runs at start")

		store.mu.Lock()
		store.instances = deletedAgentSweepTestInstances(1)
		store.mu.Unlock()
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		require.Equal(t, 2, store.lists)
		require.Equal(t, []string{"instance-000"}, workflow.calls, "an instance that appeared since is deleted on the next interval")

		cancel()
		require.NoError(t, <-done)
	})
}
