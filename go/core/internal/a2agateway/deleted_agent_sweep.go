package a2agateway

import (
	"context"
	"errors"
	"time"

	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// deletedAgentSweepPage bounds the instances one listing of the sweep
	// returns; a sweep walks the pages until the last.
	deletedAgentSweepPage = 100
	// deletedAgentDeleteTimeout bounds one instance's deletion.
	deletedAgentDeleteTimeout = time.Minute
	// DefaultDeletedAgentSweepInterval is how often the controller deletes the
	// instances of deleted agents. The sweep always runs: an instance's runtime
	// must not outlive the agent it was created for.
	DefaultDeletedAgentSweepInterval = 5 * time.Minute
)

type deletedAgentSweepStore interface {
	ListAgentInstancesOfDeletedAgents(context.Context, string, int) ([]*apiv1alpha1.AgentInstance, error)
}

type deletedAgentSweepWorkflow interface {
	Delete(context.Context, *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error)
}

// DeletedAgentSweep deletes the instances whose agent is gone through the
// ordinary delete workflow.
//
// Deleting an AgentTemplate retires its pair and leaves its instances alone: an
// instance READY on its revision keeps its Actor in Substrate and the revision's
// ActorTemplate referenced, so a conversation nobody deletes outlives its agent
// and the runtime revision GC never collects the template. The sweep deletes
// those instances — their runtime is suspended and deleted, their revision
// released — at the interval the controller is given, so nothing is orphaned.
// An instance of an agent re-rendered or replaced under the same name keeps its
// pinned revision; the revision sweep moves it onto the current one.
type DeletedAgentSweep struct {
	store       deletedAgentSweepStore
	workflow    deletedAgentSweepWorkflow
	coordinator runtimeCoordinator
	interval    time.Duration
}

var (
	_ manager.Runnable               = (*DeletedAgentSweep)(nil)
	_ manager.LeaderElectionRunnable = (*DeletedAgentSweep)(nil)
)

// NewDeletedAgentSweep returns the sweep for the gateway New serves. It shares
// the gateway's per-instance coordination, so a deletion never overlaps a turn
// being dispatched or quiesced in this process.
func NewDeletedAgentSweep(store deletedAgentSweepStore, workflow deletedAgentSweepWorkflow, interval time.Duration) *DeletedAgentSweep {
	return newDeletedAgentSweep(store, workflow, interval, processRuntimeCoordinator)
}

func newDeletedAgentSweep(store deletedAgentSweepStore, workflow deletedAgentSweepWorkflow, interval time.Duration, coordinator runtimeCoordinator) *DeletedAgentSweep {
	return &DeletedAgentSweep{store: store, workflow: workflow, coordinator: coordinator, interval: interval}
}

func (s *DeletedAgentSweep) NeedLeaderElection() bool { return true }

func (s *DeletedAgentSweep) Start(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		s.sweep(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

// sweep visits every instance of a deleted agent once, by id, a page at a time.
// An instance the sweep could not delete stays in the listing and is visited
// again by the next sweep, never by this one.
func (s *DeletedAgentSweep) sweep(ctx context.Context) {
	afterID := ""
	for ctx.Err() == nil {
		listCtx, cancel := context.WithTimeout(ctx, time.Minute)
		instances, err := s.store.ListAgentInstancesOfDeletedAgents(listCtx, afterID, deletedAgentSweepPage)
		cancel()
		if err != nil {
			logging.FromContext(ctx).ErrorContext(ctx, "failed to list agent instances of deleted agents", "error", err)
			return
		}
		for _, instance := range instances {
			if ctx.Err() != nil {
				return
			}
			s.delete(ctx, instance)
		}
		if len(instances) < deletedAgentSweepPage {
			return
		}
		afterID = instances[len(instances)-1].GetId()
	}
}

// delete deletes one instance. The instance's quiesce lock is taken without
// waiting: a turn being dispatched or quiesced wins, and the next sweep looks
// again. The delete workflow's admission fences a concurrent lifecycle call and
// supersedes an unclaimed one, so a conflict is left for the next sweep. A turn
// the delete settled meanwhile, and an agent active again under the name since
// the listing, drop out of the next listing on their own.
func (s *DeletedAgentSweep) delete(ctx context.Context, instance *apiv1alpha1.AgentInstance) {
	release, ok := s.coordinator.TryQuiesce(instance.GetId())
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, deletedAgentDeleteTimeout)
	defer cancel()
	switch _, err := s.workflow.Delete(ctx, instance); {
	case errors.Is(err, database.ErrConflict), errors.Is(err, database.ErrNotFound), errors.Is(err, database.ErrFailedPrecondition):
		logging.FromContext(ctx).DebugContext(ctx, "agent instance of a deleted agent left for the next sweep",
			"instance_id", instance.GetId(), "error", err)
	case err != nil:
		logging.FromContext(ctx).ErrorContext(ctx, "failed to delete the agent instance of a deleted agent",
			"instance_id", instance.GetId(), "error", err)
	default:
		logging.FromContext(ctx).InfoContext(ctx, "deleted the agent instance of a deleted agent", "instance_id", instance.GetId())
	}
}
