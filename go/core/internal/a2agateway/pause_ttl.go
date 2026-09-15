package a2agateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// pauseTTLSweepLimit bounds the candidates one sweep takes on; the rest wait
	// for the next.
	pauseTTLSweepLimit = 256
	// pauseTTLSuspendTimeout bounds one runtime's suspend, upload included.
	pauseTTLSuspendTimeout = 5 * time.Minute
	// pauseTTLMaxInterval keeps the sweep responsive for long TTLs.
	pauseTTLMaxInterval = 30 * time.Second
)

type pauseTTLStore interface {
	ListAgentInstancesPausedBefore(context.Context, time.Time, int) ([]database.PausedAgentInstance, error)
	GetAgentInstanceTask(context.Context, string, string, *int) (*a2atype.Task, error)
	RecordAgentInstanceTaskSnapshot(context.Context, string, string, a2atype.TaskState, *database.AgentInstanceTaskSnapshot) error
}

type pauseTTLWorkflow interface {
	Idle(context.Context, *apiv1alpha1.AgentInstance) (bool, error)
	Quiesce(context.Context, *apiv1alpha1.AgentInstance) (*database.AgentInstanceTaskSnapshot, error)
}

// PauseTTL bounds how long a runtime paused for a person's input stays on its
// worker.
//
// A turn that ends in input-required or auth-required pauses the runtime where
// it ran: a checkpoint on the worker's node that the reply resumes in place. The
// node holds the only copy, and a person may take hours to answer; a node that
// goes away in the meantime takes the conversation's runtime state with it, and
// the reply finds nothing to resume. Once a pause is older than the TTL the
// runtime is suspended instead — the checkpoint uploaded to the snapshot store
// and recorded on the task the way a terminal turn's is — so the reply, whenever
// it comes, restores it from there. A reply within the TTL still gets the
// in-place resume.
type PauseTTL struct {
	store       pauseTTLStore
	workflow    pauseTTLWorkflow
	coordinator runtimeCoordinator
	ttl         time.Duration
}

var (
	_ manager.Runnable               = (*PauseTTL)(nil)
	_ manager.LeaderElectionRunnable = (*PauseTTL)(nil)
)

// NewPauseTTL returns the sweep for the gateway New serves. It shares the
// gateway's per-instance coordination, so a suspend never overlaps a turn
// being dispatched or quiesced.
func NewPauseTTL(store pauseTTLStore, workflow pauseTTLWorkflow, ttl time.Duration) *PauseTTL {
	return newPauseTTL(store, workflow, ttl, processRuntimeCoordinator)
}

func newPauseTTL(store pauseTTLStore, workflow pauseTTLWorkflow, ttl time.Duration, coordinator runtimeCoordinator) *PauseTTL {
	return &PauseTTL{store: store, workflow: workflow, coordinator: coordinator, ttl: ttl}
}

func (p *PauseTTL) NeedLeaderElection() bool { return true }

// interval is half the TTL, so a pause is suspended within half a TTL of
// expiring, and at most pauseTTLMaxInterval.
func (p *PauseTTL) interval() time.Duration {
	return max(min(p.ttl/2, pauseTTLMaxInterval), time.Second)
}

func (p *PauseTTL) Start(ctx context.Context) error {
	ticker := time.NewTicker(p.interval())
	defer ticker.Stop()
	for ctx.Err() == nil {
		p.sweep(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func (p *PauseTTL) sweep(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, time.Minute)
	paused, err := p.store.ListAgentInstancesPausedBefore(listCtx, time.Now().Add(-p.ttl), pauseTTLSweepLimit)
	cancel()
	if err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "failed to list paused agent instances", "error", err)
		return
	}
	for _, candidate := range paused {
		if ctx.Err() != nil {
			return
		}
		if err := p.suspend(ctx, candidate); err != nil {
			logging.FromContext(ctx).ErrorContext(ctx, "failed to suspend paused agent instance runtime", "error", err,
				"instance_id", candidate.Instance.GetId(), "task_id", candidate.TaskID)
		}
	}
}

// suspend makes one paused runtime durable. The instance's quiesce lock is
// taken without waiting: a turn being dispatched or quiesced wins, and the
// next sweep looks again. Under the lock the task is read back, because a
// reply admitted since the listing has moved it on, and the runtime is
// suspended only when it is idle — a runtime running a turn this process does
// not see is left to it.
func (p *PauseTTL) suspend(ctx context.Context, candidate database.PausedAgentInstance) error {
	instance := candidate.Instance
	release, ok := p.coordinator.TryQuiesce(instance.GetId())
	if !ok {
		return nil
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, pauseTTLSuspendTimeout)
	defer cancel()
	noHistory := 0
	task, err := p.store.GetAgentInstanceTask(ctx, instance.GetId(), candidate.TaskID, &noHistory)
	if errors.Is(err, database.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get paused task: %w", err)
	}
	if !requiresInput(task.Status.State) {
		return nil
	}
	idle, err := p.workflow.Idle(ctx, instance)
	if err != nil {
		return fmt.Errorf("inspect AgentInstance runtime: %w", err)
	}
	if !idle {
		logging.FromContext(ctx).DebugContext(ctx, "paused agent instance runtime is not idle; left for the next sweep",
			"instance_id", instance.GetId(), "task_id", task.ID)
		return nil
	}
	snapshot, err := p.workflow.Quiesce(ctx, instance)
	if err != nil {
		return fmt.Errorf("quiesce AgentInstance runtime: %w", err)
	}
	err = p.store.RecordAgentInstanceTaskSnapshot(ctx, instance.GetId(), string(task.ID), task.Status.State, snapshot)
	if errors.Is(err, database.ErrConflict) || errors.Is(err, database.ErrNotFound) {
		// The turn moved on, or the instance went away, while the runtime was
		// being suspended: the boundary that counts is the one the turn records.
		return nil
	}
	if err != nil {
		return fmt.Errorf("record paused task snapshot: %w", err)
	}
	logging.FromContext(ctx).InfoContext(ctx, "suspended agent instance runtime paused for input beyond the TTL",
		"instance_id", instance.GetId(), "task_id", task.ID, "paused_for", time.Since(candidate.PausedAt).Round(time.Second).String(),
		"snapshot_uri", snapshot.URI)
	return nil
}
