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
	// revisionSweepPage bounds the instances one listing of the sweep returns;
	// a sweep walks the pages until the last.
	revisionSweepPage = 100
	// revisionRepointTimeout bounds one runtime's move.
	revisionRepointTimeout = time.Minute
)

type revisionSweepStore interface {
	ListAgentInstancesOnSupersededRevisions(context.Context, string, int) ([]*apiv1alpha1.AgentInstance, error)
}

type revisionSweepWorkflow interface {
	RepointQuiesced(context.Context, *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error)
}

// RevisionSweep moves the quiesced runtimes of instances whose revision their
// agent has superseded onto the current one.
//
// A turn moves its own runtime before it wakes it, so a conversation that is
// written to takes its agent's current revision by itself. A conversation
// nobody writes to again keeps its revision pinned, and with it the
// ActorTemplate rendered for it, with everything an older release put there:
// the runtime revision GC collects a revision only once no instance references
// it. The sweep moves those runtimes while they are suspended, at the interval
// the controller is given, through the same repoint a turn uses.
type RevisionSweep struct {
	store       revisionSweepStore
	workflow    revisionSweepWorkflow
	coordinator runtimeCoordinator
	interval    time.Duration
}

var (
	_ manager.Runnable               = (*RevisionSweep)(nil)
	_ manager.LeaderElectionRunnable = (*RevisionSweep)(nil)
)

// NewRevisionSweep returns the sweep for the gateway New serves. It shares the
// gateway's per-instance coordination, so a move never overlaps a turn being
// dispatched or quiesced in this process.
func NewRevisionSweep(store revisionSweepStore, workflow revisionSweepWorkflow, interval time.Duration) *RevisionSweep {
	return newRevisionSweep(store, workflow, interval, processRuntimeCoordinator)
}

func newRevisionSweep(store revisionSweepStore, workflow revisionSweepWorkflow, interval time.Duration, coordinator runtimeCoordinator) *RevisionSweep {
	return &RevisionSweep{store: store, workflow: workflow, coordinator: coordinator, interval: interval}
}

func (s *RevisionSweep) NeedLeaderElection() bool { return true }

func (s *RevisionSweep) Start(ctx context.Context) error {
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

// sweep visits every instance on a superseded revision once, by id, a page at
// a time. An instance the sweep could not move stays in the listing and is
// visited again by the next sweep, never by this one.
func (s *RevisionSweep) sweep(ctx context.Context) {
	afterID := ""
	for ctx.Err() == nil {
		listCtx, cancel := context.WithTimeout(ctx, time.Minute)
		instances, err := s.store.ListAgentInstancesOnSupersededRevisions(listCtx, afterID, revisionSweepPage)
		cancel()
		if err != nil {
			logging.FromContext(ctx).ErrorContext(ctx, "failed to list agent instances on superseded revisions", "error", err)
			return
		}
		for _, instance := range instances {
			if ctx.Err() != nil {
				return
			}
			s.repoint(ctx, instance)
		}
		if len(instances) < revisionSweepPage {
			return
		}
		afterID = instances[len(instances)-1].GetId()
	}
}

// repoint moves one runtime. The instance's quiesce lock is taken without
// waiting: a turn being dispatched or quiesced wins, and the next sweep looks
// again. A lifecycle operation that took the instance meanwhile wins the same
// way.
func (s *RevisionSweep) repoint(ctx context.Context, instance *apiv1alpha1.AgentInstance) {
	release, ok := s.coordinator.TryQuiesce(instance.GetId())
	if !ok {
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, revisionRepointTimeout)
	defer cancel()
	moved, err := s.workflow.RepointQuiesced(ctx, instance)
	switch {
	case errors.Is(err, database.ErrConflict):
		logging.FromContext(ctx).DebugContext(ctx, "agent instance left for its lifecycle operation; its runtime stays on the superseded revision for now",
			"instance_id", instance.GetId(), "revision", instance.GetPreparedRevision())
	case err != nil:
		logging.FromContext(ctx).ErrorContext(ctx, "failed to move the agent instance runtime to its agent's current revision",
			"error", err, "instance_id", instance.GetId(), "revision", instance.GetPreparedRevision())
	case moved.GetPreparedRevision() != instance.GetPreparedRevision():
		logging.FromContext(ctx).InfoContext(ctx, "moved the agent instance runtime to its agent's current revision",
			"instance_id", instance.GetId(), "revision", moved.GetPreparedRevision(), "superseded_revision", instance.GetPreparedRevision())
	}
}
