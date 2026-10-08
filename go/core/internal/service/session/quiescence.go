package session

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/pkg/logging"

	"github.com/kagent-dev/kagent/go/core/internal/database"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ manager.Runnable = (*ActorWorkflow)(nil)
var _ manager.LeaderElectionRunnable = (*ActorWorkflow)(nil)

// Every API replica can process idle work; PostgreSQL grants each claim once.
func (*ActorWorkflow) NeedLeaderElection() bool { return false }

// Start pauses or suspends idle sessions independently of task publication.
// A periodic scan discovers settled work across API replicas and restarts.
// Workers are bounded; task reads never wait for them.
func (w *ActorWorkflow) Start(ctx context.Context) error {
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			timer := time.NewTicker(time.Second)
			defer timer.Stop()
			for ctx.Err() == nil {
				work, err := w.store.ClaimSessionQuiescence(ctx)
				if err == nil {
					w.quiesceIdleSession(ctx, work)
					continue
				}
				if !errors.Is(err, database.ErrNotFound) && ctx.Err() == nil {
					logging.FromContext(ctx).ErrorContext(ctx, "claim runtime boundary", "error", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
				}
			}
		})
	}
	workers.Wait()
	w.settling.Wait()
	return nil
}

func (w *ActorWorkflow) quiesceIdleSession(ctx context.Context, work *database.SessionQuiescence) {
	runtimeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	var snapshot *database.SessionTaskSnapshot
	var err error
	if work.State.Terminal() {
		snapshot, err = w.Quiesce(runtimeCtx, work.Session)
	} else {
		err = w.Pause(runtimeCtx, work.Session)
	}
	cancel()
	if err != nil {
		// No timeout-based takeover: the Substrate request may still complete.
		// Keep admission closed until the Actor's own state settles the outcome.
		logging.FromContext(ctx).ErrorContext(ctx, "runtime boundary outcome unknown", "session_id", work.Session.Id, "task_id", work.TaskID, "version", work.Version, "error", err)
		w.settling.Go(func() { w.settleIdleWork(ctx, work) })
		return
	}
	w.finishIdleWork(ctx, work, snapshot)
}

// settleIdleWork resolves a claim whose Pause or Quiesce failed from the state
// Substrate reports for the Actor, so the session does not refuse sends forever.
// A suspended Actor's external snapshot finishes the claim as Quiesce would
// have, and a paused Actor finishes a pause. While the Actor is still in
// transition, or Substrate cannot answer, the claim is kept and read again with
// a backoff. A running Actor did not take the request, or Substrate abandoned
// it: the request is sent once more and its outcome settled the same way. Any
// other state, a crashed or missing Actor included, or an Actor still running
// after the retry, releases the claim without a snapshot and records why on
// the session: the next send takes the runtime as it is.
func (w *ActorWorkflow) settleIdleWork(ctx context.Context, work *database.SessionQuiescence) {
	log := logging.FromContext(ctx).With("session_id", work.Session.Id, "task_id", work.TaskID, "version", work.Version)
	var retryErr error
	retried := false
	for delay := w.settleDelay; ; delay = min(2*delay, time.Minute) {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		actor, snapshot, err := w.boundaryActor(readCtx, work.Session)
		cancel()
		state := actor.GetStatus().GetState()
		switch {
		case err != nil:
			log.WarnContext(ctx, "runtime boundary still unknown", "error", err, "retry_in", delay)
		case actor != nil && inTransition(state):
			log.InfoContext(ctx, "runtime boundary still in transition", "actor_state", state, "retry_in", delay)
		case snapshot != nil:
			log.InfoContext(ctx, "runtime boundary settled from suspended Actor", "snapshot_uri", snapshot.URI)
			w.finishIdleWork(ctx, work, snapshot)
			return
		case state == ateapipb.ActorState_ACTOR_STATE_PAUSED && !work.State.Terminal():
			log.InfoContext(ctx, "runtime boundary settled from paused Actor")
			w.finishIdleWork(ctx, work, nil)
			return
		case state == ateapipb.ActorState_ACTOR_STATE_RUNNING && !retried:
			retried = true
			log.InfoContext(ctx, "runtime boundary retried on running Actor")
			snapshot, retryErr = w.retryIdleWork(ctx, work)
			if retryErr == nil {
				log.InfoContext(ctx, "runtime boundary settled by its retry")
				w.finishIdleWork(ctx, work, snapshot)
				return
			}
			log.WarnContext(ctx, "runtime boundary retry failed", "error", retryErr, "retry_in", delay)
		default:
			failure := quiescenceFailure(work, actor, retryErr)
			log.WarnContext(ctx, "runtime boundary released without snapshot", "actor_state", state, "actor_found", actor != nil, "reason", failure.GetReason(), "message", failure.GetMessage())
			w.recordIdleWork(ctx, work, func(ctx context.Context) error { return w.store.ReleaseSessionQuiescence(ctx, work, failure) })
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// retryIdleWork sends work's Pause or Quiesce once more, after the Actor was
// found running again.
func (w *ActorWorkflow) retryIdleWork(ctx context.Context, work *database.SessionQuiescence) (*database.SessionTaskSnapshot, error) {
	runtimeCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if work.State.Terminal() {
		return w.Quiesce(runtimeCtx, work.Session)
	}
	return nil, w.Pause(runtimeCtx, work.Session)
}

// quiescenceFailure explains a boundary released without a pause or a
// snapshot: the retry's error when the request was sent again, otherwise the
// state the Actor was found in.
func quiescenceFailure(work *database.SessionQuiescence, actor *ateapipb.Actor, retryErr error) *apiv1alpha1.QuiescenceFailure {
	if retryErr != nil {
		reason := "PauseFailed"
		if work.State.Terminal() {
			reason = "SuspendFailed"
		}
		return &apiv1alpha1.QuiescenceFailure{Reason: reason, Message: fmt.Sprintf("retry after a failed request: %v", retryErr)}
	}
	if actor == nil {
		return &apiv1alpha1.QuiescenceFailure{Reason: "RuntimeUnavailable", Message: "the runtime's Actor was not found"}
	}
	return &apiv1alpha1.QuiescenceFailure{Reason: "RuntimeUnavailable", Message: fmt.Sprintf("the runtime's Actor is %s", actor.GetStatus().GetState())}
}

func inTransition(state ateapipb.ActorState) bool {
	switch state {
	case ateapipb.ActorState_ACTOR_STATE_UNSPECIFIED, ateapipb.ActorState_ACTOR_STATE_RESUMING, ateapipb.ActorState_ACTOR_STATE_SUSPENDING,
		ateapipb.ActorState_ACTOR_STATE_PAUSING, ateapipb.ActorState_ACTOR_STATE_DELETING, ateapipb.ActorState_ACTOR_STATE_REVERTING:
		return true
	}
	return false
}

// finishIdleWork records the claim's outcome. Keep a known snapshot until its
// reference is stored.
func (w *ActorWorkflow) finishIdleWork(ctx context.Context, work *database.SessionQuiescence, snapshot *database.SessionTaskSnapshot) {
	w.recordIdleWork(ctx, work, func(ctx context.Context) error { return w.store.FinishSessionQuiescence(ctx, work, snapshot) })
}

// recordIdleWork retries database failures without repeating runtime work;
// give shutdown one bounded completion attempt.
func (w *ActorWorkflow) recordIdleWork(ctx context.Context, work *database.SessionQuiescence, record func(context.Context) error) {
	var err error
	for delay := 100 * time.Millisecond; ; delay = min(2*delay, 5*time.Second) {
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = record(finishCtx)
		finishCancel()
		if err == nil {
			return
		}
		logging.FromContext(ctx).ErrorContext(ctx, "record idle runtime outcome", "session_id", work.Session.Id, "version", work.Version, "error", err)
		if errors.Is(err, database.ErrNotFound) || errors.Is(err, database.ErrConflict) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}
