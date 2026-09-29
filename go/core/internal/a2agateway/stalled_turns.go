package a2agateway

import (
	"context"
	"fmt"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// stalledTurnsSweepLimit bounds the candidates one sweep takes on; the rest
	// wait for the next.
	stalledTurnsSweepLimit = 256
	// stalledTurnsMaxInterval keeps the sweep responsive for long timeouts.
	stalledTurnsMaxInterval = time.Minute
)

type stalledTurnsStore interface {
	ListAgentInstanceTasksWorkingBefore(context.Context, time.Time, int) ([]database.StalledAgentInstanceTask, error)
	InterruptActiveAgentInstanceTask(context.Context, string, string) (bool, error)
}

// StalledTurns ends turns that stopped making progress.
//
// A turn's events reach its task only through the run of this gateway that
// dispatched or observes it. A runtime lost after its first event (a spot
// reclaim, a worker roll) or a controller restart mid-turn ends that run, or
// takes it with the process, and leaves the task working: the instance keeps
// it as its active task, refuses every later message as a conflict, and every
// client shows a turn that never ends. A submitted task is bounded by the
// dispatch grace period; a working one had no bound. Once a working task has
// recorded no event for longer than the timeout and no run of this gateway
// follows it, the sweep fails it with the interruption message, and the
// instance takes the next message.
type StalledTurns struct {
	store       stalledTurnsStore
	gateway     *Gateway
	coordinator runtimeCoordinator
	timeout     time.Duration
}

var (
	_ manager.Runnable               = (*StalledTurns)(nil)
	_ manager.LeaderElectionRunnable = (*StalledTurns)(nil)
)

// NewStalledTurns returns the sweep for the gateway handler New returned: only
// that gateway knows which turns a run still follows.
func NewStalledTurns(store stalledTurnsStore, handler a2asrv.RequestHandler, timeout time.Duration) (*StalledTurns, error) {
	intercepted, ok := handler.(*a2asrv.InterceptedHandler)
	if !ok {
		return nil, fmt.Errorf("stalled turns need the handler a2agateway.New returned, got %T", handler)
	}
	gateway, ok := intercepted.Handler.(*Gateway)
	if !ok {
		return nil, fmt.Errorf("stalled turns need the handler a2agateway.New returned, got %T", intercepted.Handler)
	}
	return newStalledTurns(store, gateway, timeout), nil
}

func newStalledTurns(store stalledTurnsStore, gateway *Gateway, timeout time.Duration) *StalledTurns {
	return &StalledTurns{store: store, gateway: gateway, coordinator: gateway.coordinator, timeout: timeout}
}

func (s *StalledTurns) NeedLeaderElection() bool { return true }

// interval is half the timeout, so a stalled turn ends within one and a half
// timeouts of its last event, and at most stalledTurnsMaxInterval.
func (s *StalledTurns) interval() time.Duration {
	return max(min(s.timeout/2, stalledTurnsMaxInterval), time.Second)
}

func (s *StalledTurns) Start(ctx context.Context) error {
	ticker := time.NewTicker(s.interval())
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

func (s *StalledTurns) sweep(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, time.Minute)
	stalled, err := s.store.ListAgentInstanceTasksWorkingBefore(listCtx, time.Now().Add(-s.timeout), stalledTurnsSweepLimit)
	cancel()
	if err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "failed to list stalled agent instance tasks", "error", err)
		return
	}
	for _, candidate := range stalled {
		if ctx.Err() != nil {
			return
		}
		if err := s.end(ctx, candidate); err != nil {
			logging.FromContext(ctx).ErrorContext(ctx, "failed to end stalled agent instance task", "error", err,
				"instance_id", candidate.InstanceID, "task_id", candidate.TaskID)
		}
	}
}

// end fails one stalled task. The instance's quiesce lock is taken without
// waiting: a turn being dispatched or quiesced wins, and the next sweep looks
// again. A task a run of this gateway still follows is left to the run, however
// quiet it is: the run records the turn's next event, or its end.
func (s *StalledTurns) end(ctx context.Context, candidate database.StalledAgentInstanceTask) error {
	release, ok := s.coordinator.TryQuiesce(candidate.InstanceID)
	if !ok {
		return nil
	}
	defer release()
	if _, owned := s.gateway.taskRun(candidate.InstanceID, a2atype.TaskID(candidate.TaskID)); owned {
		return nil
	}
	interrupted, err := s.store.InterruptActiveAgentInstanceTask(ctx, candidate.InstanceID, candidate.TaskID)
	if err != nil {
		return fmt.Errorf("interrupt stalled task: %w", err)
	}
	if interrupted {
		logging.FromContext(ctx).InfoContext(ctx, "failed a working agent instance task that recorded no event beyond the stalled-turn timeout",
			"instance_id", candidate.InstanceID, "task_id", candidate.TaskID,
			"stalled_for", time.Since(candidate.LastEventAt).Round(time.Second).String())
	}
	return nil
}
