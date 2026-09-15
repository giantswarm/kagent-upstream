package a2agateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/stretchr/testify/require"
)

const pauseTTLTestTTL = 2 * time.Minute

type pauseTTLTestStore struct {
	mu        sync.Mutex
	paused    []database.PausedAgentInstance
	listErr   error
	lists     int
	tasks     map[string]*a2atype.Task
	taskErr   error
	recorded  []pauseTTLTestRecord
	recordErr error
}

type pauseTTLTestRecord struct {
	instanceID, taskID string
	state              a2atype.TaskState
	snapshot           *database.AgentInstanceTaskSnapshot
}

func (s *pauseTTLTestStore) ListAgentInstancesPausedBefore(_ context.Context, cutoff time.Time, limit int) ([]database.PausedAgentInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lists++
	if s.listErr != nil {
		return nil, s.listErr
	}
	var result []database.PausedAgentInstance
	for _, candidate := range s.paused {
		if candidate.PausedAt.Before(cutoff) && len(result) < limit {
			result = append(result, candidate)
		}
	}
	return result, nil
}

func (s *pauseTTLTestStore) GetAgentInstanceTask(_ context.Context, _, taskID string, historyLength *int) (*a2atype.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if historyLength == nil || *historyLength != 0 {
		return nil, errors.New("the sweep needs no history")
	}
	if s.taskErr != nil {
		return nil, s.taskErr
	}
	task, ok := s.tasks[taskID]
	if !ok {
		return nil, database.ErrNotFound
	}
	return task, nil
}

func (s *pauseTTLTestStore) RecordAgentInstanceTaskSnapshot(_ context.Context, instanceID, taskID string, state a2atype.TaskState, snapshot *database.AgentInstanceTaskSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recordErr != nil {
		return s.recordErr
	}
	s.recorded = append(s.recorded, pauseTTLTestRecord{instanceID: instanceID, taskID: taskID, state: state, snapshot: snapshot})
	for i, candidate := range s.paused {
		if candidate.Instance.GetId() == instanceID && candidate.TaskID == taskID {
			s.paused = append(s.paused[:i:i], s.paused[i+1:]...)
			break
		}
	}
	return nil
}

func (s *pauseTTLTestStore) records() []pauseTTLTestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]pauseTTLTestRecord{}, s.recorded...)
}

type pauseTTLTestWorkflow struct {
	mu         sync.Mutex
	idle       bool
	idleErr    error
	quiesceErr map[string]error
	quiesced   []string
	snapshot   *database.AgentInstanceTaskSnapshot
}

func (w *pauseTTLTestWorkflow) Idle(context.Context, *apiv1alpha1.AgentInstance) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.idle, w.idleErr
}

func (w *pauseTTLTestWorkflow) Quiesce(_ context.Context, instance *apiv1alpha1.AgentInstance) (*database.AgentInstanceTaskSnapshot, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.quiesced = append(w.quiesced, instance.GetId())
	if err := w.quiesceErr[instance.GetId()]; err != nil {
		return nil, err
	}
	return w.snapshot, nil
}

func (w *pauseTTLTestWorkflow) quiescedIDs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string{}, w.quiesced...)
}

func pauseTTLTestCandidate(id, taskID string, pausedFor time.Duration) database.PausedAgentInstance {
	return database.PausedAgentInstance{
		Instance: &apiv1alpha1.AgentInstance{Id: id, ContextId: gatewayTestContextID, PreparedRevision: "revision-1",
			State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY},
		TaskID:   taskID,
		PausedAt: time.Now().Add(-pausedFor),
	}
}

func pauseTTLTestTask(taskID string, state a2atype.TaskState) *a2atype.Task {
	return &a2atype.Task{ID: a2atype.TaskID(taskID), ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: state}}
}

func pauseTTLTestFixture() (*pauseTTLTestStore, *pauseTTLTestWorkflow) {
	store := &pauseTTLTestStore{
		paused: []database.PausedAgentInstance{pauseTTLTestCandidate(gatewayTestID, "task-1", 3*time.Minute)},
		tasks:  map[string]*a2atype.Task{"task-1": pauseTTLTestTask("task-1", a2atype.TaskStateInputRequired)},
	}
	workflow := &pauseTTLTestWorkflow{idle: true, snapshot: &database.AgentInstanceTaskSnapshot{Atespace: "team-a", URI: "s3://snapshots/paused", ContentScope: "FULL"}}
	return store, workflow
}

func TestPauseTTLSuspendsAPauseOlderThanTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		store, workflow := pauseTTLTestFixture()
		store.paused = append(store.paused, pauseTTLTestCandidate("fresh", "task-2", time.Minute))
		store.tasks["task-2"] = pauseTTLTestTask("task-2", a2atype.TaskStateAuthRequired)
		sweep := newPauseTTL(store, workflow, pauseTTLTestTTL, &memoryRuntimeCoordinator{})
		require.True(t, sweep.NeedLeaderElection())
		done := make(chan error, 1)
		go func() { done <- sweep.Start(ctx) }()
		synctest.Wait()
		require.Equal(t, []string{gatewayTestID}, workflow.quiescedIDs(), "startup sweeps at once; a pause within the TTL stays on its worker")
		require.Equal(t, []pauseTTLTestRecord{{instanceID: gatewayTestID, taskID: "task-1", state: a2atype.TaskStateInputRequired, snapshot: workflow.snapshot}}, store.records())

		time.Sleep(sweep.interval())
		synctest.Wait()
		require.Equal(t, []string{gatewayTestID}, workflow.quiescedIDs(), "a recorded pause is not suspended again")
		time.Sleep(time.Minute + sweep.interval())
		synctest.Wait()
		require.Equal(t, []string{gatewayTestID, "fresh"}, workflow.quiescedIDs(), "the pause is suspended once it is older than the TTL")
		require.Equal(t, a2atype.TaskStateAuthRequired, store.records()[1].state)
		cancel()
		require.NoError(t, <-done)
	})
}

func TestPauseTTLLeavesATurnInFlightAndRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		store, workflow := pauseTTLTestFixture()
		coordinator := &memoryRuntimeCoordinator{}
		release := coordinator.RuntimeCall(gatewayTestID)
		sweep := newPauseTTL(store, workflow, pauseTTLTestTTL, coordinator)
		done := make(chan error, 1)
		go func() { done <- sweep.Start(ctx) }()
		synctest.Wait()
		require.Empty(t, workflow.quiescedIDs(), "a dispatch in flight wins")
		release()
		time.Sleep(sweep.interval())
		synctest.Wait()
		require.Equal(t, []string{gatewayTestID}, workflow.quiescedIDs(), "the next sweep suspends")
		cancel()
		require.NoError(t, <-done)
	})
}

func TestPauseTTLSkipsWhatIsNotAPausedIdleRuntime(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*pauseTTLTestStore, *pauseTTLTestWorkflow)
	}{
		{name: "a task that moved on", setup: func(store *pauseTTLTestStore, _ *pauseTTLTestWorkflow) {
			store.tasks["task-1"] = pauseTTLTestTask("task-1", a2atype.TaskStateWorking)
		}},
		{name: "a task that is gone", setup: func(store *pauseTTLTestStore, _ *pauseTTLTestWorkflow) {
			delete(store.tasks, "task-1")
		}},
		{name: "a runtime that is not idle", setup: func(_ *pauseTTLTestStore, workflow *pauseTTLTestWorkflow) {
			workflow.idle = false
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, workflow := pauseTTLTestFixture()
			test.setup(store, workflow)
			sweep := newPauseTTL(store, workflow, pauseTTLTestTTL, &memoryRuntimeCoordinator{})
			sweep.sweep(t.Context())
			require.Empty(t, workflow.quiescedIDs())
			require.Empty(t, store.records())
		})
	}
}

func TestPauseTTLToleratesFailuresPerCandidate(t *testing.T) {
	store, workflow := pauseTTLTestFixture()
	store.paused = append(store.paused, pauseTTLTestCandidate("second", "task-2", 3*time.Minute))
	store.tasks["task-2"] = pauseTTLTestTask("task-2", a2atype.TaskStateInputRequired)
	workflow.quiesceErr = map[string]error{gatewayTestID: errors.New("Substrate unavailable")}
	sweep := newPauseTTL(store, workflow, pauseTTLTestTTL, &memoryRuntimeCoordinator{})
	sweep.sweep(t.Context())
	require.Equal(t, []string{gatewayTestID, "second"}, workflow.quiescedIDs(), "a failed candidate does not block the next")
	require.Equal(t, []pauseTTLTestRecord{{instanceID: "second", taskID: "task-2", state: a2atype.TaskStateInputRequired, snapshot: workflow.snapshot}}, store.records())

	store, workflow = pauseTTLTestFixture()
	store.recordErr = database.ErrConflict
	sweep = newPauseTTL(store, workflow, pauseTTLTestTTL, &memoryRuntimeCoordinator{})
	sweep.sweep(t.Context())
	require.Equal(t, []string{gatewayTestID}, workflow.quiescedIDs(), "a turn that moved on under the suspend keeps its own boundary")

	store, workflow = pauseTTLTestFixture()
	store.listErr = errors.New("database unavailable")
	sweep = newPauseTTL(store, workflow, pauseTTLTestTTL, &memoryRuntimeCoordinator{})
	sweep.sweep(t.Context())
	require.Empty(t, workflow.quiescedIDs())
}

func TestPauseTTLInterval(t *testing.T) {
	for ttl, want := range map[time.Duration]time.Duration{
		2 * time.Minute:  30 * time.Second,
		20 * time.Second: 10 * time.Second,
		time.Second:      time.Second,
		time.Hour:        30 * time.Second,
	} {
		require.Equal(t, want, (&PauseTTL{ttl: ttl}).interval(), "ttl %s", ttl)
	}
}

// lockProbeDialer records whether the instance's quiesce lock is held while
// the gateway dials the runtime.
type lockProbeDialer struct {
	client       *a2aclient.Client
	coordinator  runtimeCoordinator
	lockedOnDial bool
}

func (d *lockProbeDialer) Dial(_ context.Context, instance *apiv1alpha1.AgentInstance) (*a2aclient.Client, error) {
	release, free := d.coordinator.TryQuiesce(instance.GetId())
	if free {
		release()
	}
	d.lockedOnDial = !free
	return d.client, nil
}

func TestGatewayDispatchesUnderTheRuntimeCallLock(t *testing.T) {
	coordinator := &memoryRuntimeCoordinator{}
	dialer := &lockProbeDialer{client: gatewayTestClient(t, &gatewayTestRuntime{}), coordinator: coordinator}
	gateway := newGateway(&gatewayTestStore{instance: gatewayTestInstance()}, &gatewayTestAuthorizer{}, dialer, &gatewayTestWorkflow{}, gatewayTestURL, coordinator)
	_, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest())
	require.NoError(t, err)
	require.True(t, dialer.lockedOnDial, "no suspend may start while the runtime is being dialled")
	release, free := coordinator.TryQuiesce(gatewayTestID)
	require.True(t, free, "the lock is released once the run has started")
	release()

	dialer.lockedOnDial = false
	for event, err := range gateway.SendStreamingMessage(gatewayTestContext(), gatewayTestRequest()) {
		require.NoError(t, err)
		require.NotNil(t, event)
	}
	require.True(t, dialer.lockedOnDial)
	release, free = coordinator.TryQuiesce(gatewayTestID)
	require.True(t, free)
	release()
}
