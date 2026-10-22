package session

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/uuid"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type workflowStore interface {
	ClaimSessionQuiescence(context.Context, time.Duration, time.Duration, []string) (*database.SessionQuiescence, error)
	RenewSessionQuiescence(context.Context, *database.SessionQuiescence, time.Duration) error
	HasPendingSessionQuiescence(context.Context) (bool, error)
	FinishSessionQuiescence(context.Context, *database.SessionQuiescence, *database.SessionTaskSnapshot) error
	ReleaseSessionQuiescence(context.Context, *database.SessionQuiescence, *apiv1alpha1.QuiescenceFailure) error
	NextQuiescenceFencingGeneration(context.Context) (int64, error)
	GetSessionForRuntime(context.Context, string, string) (*apiv1alpha1.Session, error)
	GetSessionByID(context.Context, string) (*apiv1alpha1.Session, error)
	GetRuntimeEgressGrant(context.Context, string) (egress.Access, error)
	GetSessionCheckpointSnapshot(context.Context, string, string) (*database.SessionTaskSnapshot, string, error)
	GetRuntimeRevision(context.Context, string) (*database.RuntimeRevision, error)
	GetCurrentRuntimeRevision(context.Context, string) (*database.RuntimeRevision, error)
	RepointSessionOperation(context.Context, string, uuid.UUID, uuid.UUID, string) error
	RepointSession(context.Context, string, string, string) (*apiv1alpha1.Session, error)
	RepointToolsetSessionOperation(context.Context, string, uuid.UUID, uuid.UUID, database.RuntimeRevision) error
	RepointToolsetSession(context.Context, string, string, database.RuntimeRevision) (*apiv1alpha1.Session, error)
	BeginSessionOperation(context.Context, string, apiv1alpha1.RuntimeOperation) (*database.SessionOperation, error)
	ClaimSessionOperation(context.Context, string, uuid.UUID, uuid.UUID) (bool, error)
	ReleaseRuntimeOperation(context.Context, string, uuid.UUID, uuid.UUID) error
	CancelSessionOperation(context.Context, string, uuid.UUID, uuid.UUID) (*apiv1alpha1.Session, error)
	FinishSessionOperation(context.Context, string, uuid.UUID, uuid.UUID, string, string, string) (*apiv1alpha1.Session, error)
	FailSessionOperation(context.Context, string, uuid.UUID, uuid.UUID, *apiv1alpha1.Failure) (*apiv1alpha1.Session, error)
	GetSessionOperation(context.Context, string, uuid.UUID) (*database.SessionOperation, error)
	GetCurrentSessionOperation(context.Context, string) (*database.SessionOperation, error)
	HasSessionTasks(context.Context, string) (bool, error)
}

type actorClient interface {
	substrate.LifecycleClient
	PauseActor(context.Context, string, string) (*ateapipb.Actor, error)
	RepointActor(context.Context, string, string, string, string) (*ateapipb.Actor, error)
	RevertActor(context.Context, string, string) (*ateapipb.Actor, error)
	ReplaceActorEgressPolicy(context.Context, string, string, *ateapipb.EgressPolicy) error
	ReadActorEgressPolicy(context.Context, string, string) (*ateapipb.EgressPolicy, error)
	UpdateActorEgressPolicyAt(context.Context, string, string, *ateapipb.EgressPolicy, *ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error)
	WriteActorEgressPolicy(context.Context, string, string, *ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error)
	ApplyActorEgressPolicy(context.Context, string, string, func(*ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error)) error
	ListAllWorkers(context.Context) ([]*ateapipb.Worker, error)
}

// ActorWorkflow runs the imperative Substrate operations behind Session
// lifecycle RPCs. Only the claiming caller issues lifecycle mutations; others
// observe current completion or receive a pending/superseded-operation error.
type ActorWorkflow struct {
	store            workflowStore
	actors           actorClient
	recoveryInterval time.Duration
	quiescenceWake   chan struct{}
	pausedRuntimeTTL time.Duration
	// credentials resolves the providers' credentials of a session.
	credentials providerAuthority
	deferred    deferrals
	// settling tracks the claims whose failed Pause or Quiesce is resolved from
	// the Actor's state; settleDelay is the first wait between its reads.
	settling    sync.WaitGroup
	settleDelay time.Duration
	// claimLease is how long a claim outlives its holder's last renewal before
	// another worker takes it over; the holder renews it every third of it.
	claimLease time.Duration
	// busyWait bounds how long a refused turn waits for its Actor's resume
	// while Substrate answers it later (resumeLater); busyRetry is the first
	// wait between resumes, of the turn's and of a lifecycle attempt's.
	busyWait  time.Duration
	busyRetry time.Duration
	// crashWait bounds how long TurnCrash asks again about an Actor still
	// RUNNING under a broken turn, every crashRetry.
	crashWait  time.Duration
	crashRetry time.Duration
	// directories tells the owner of a deleted session's volume that the
	// session's directory may go.
	directories DirectoryReleaser
	// toolsets prepares the toolset revision a session with tool images moves
	// to (toolsetRepointTarget).
	toolsets toolsetTemplates
}

// DirectoryReleaser tells the owner of a session's volume source that the
// session's directory may be deleted: the session was. kagent never deletes
// the directory or the volume itself.
type DirectoryReleaser interface {
	ReleaseSessionDirectory(context.Context, *apiv1alpha1.Session) error
}

// maxBusyRetry caps the wait between resumes Substrate answers later.
const maxBusyRetry = 5 * time.Second

// resumeLater reports an answer to ResumeActor that means not now, not never:
// no free worker while the pool rolls (ResourceExhausted), a resume a
// departing worker or a restarting ate-api cut short (Unavailable; the Actor
// is back at its snapshot) or another operation holding the Actor (Aborted),
// such as a repoint onto a newer template. Each clears on its own, and the
// same resume goes through once it has. Substrate's router parks a routed
// request on the same answers.
func resumeLater(err error) bool {
	switch status.Code(err) {
	case codes.ResourceExhausted, codes.Unavailable, codes.Aborted:
		return true
	}
	return false
}

// awaitResume runs resume until it succeeds, fails with an answer other than
// resumeLater, or ctx ends, waiting between runs from busyRetry, doubling up
// to maxBusyRetry. The answer of the last run is returned when ctx ends.
func (w *ActorWorkflow) awaitResume(ctx context.Context, resume func() error) error {
	start := time.Now()
	for delay := w.busyRetry; ; delay = min(2*delay, maxBusyRetry) {
		err := resume()
		if !resumeLater(err) {
			return err
		}
		select {
		case <-ctx.Done():
		case <-time.After(delay):
			if ctx.Err() == nil {
				continue
			}
		}
		return fmt.Errorf("still deferred by Substrate after %s: %w", time.Since(start).Round(100*time.Millisecond), err)
	}
}

type WorkflowOption func(*ActorWorkflow)

// WithPausedRuntimeTTL bounds how long a runtime paused for input stays on its
// node before the idle worker suspends it durably. Zero keeps every pause in
// place until the reply.
func WithPausedRuntimeTTL(ttl time.Duration) WorkflowOption {
	return func(w *ActorWorkflow) { w.pausedRuntimeTTL = ttl }
}

// WithCredentialAuthority names the credential provider the egress gateway
// resolves a session's provider credentials through. Without one, a session's
// provider hosts are allowed without a credential, but for the providers of
// WithActorJWTProviders.
func WithCredentialAuthority(authority string) WorkflowOption {
	return func(w *ActorWorkflow) { w.credentials.authority = authority }
}

// WithActorJWTProviders names the provider instances that verify the
// session Actor's own JWT themselves: a Bearer provider naming one gets the
// JWT the gateway mints for the Actor, bound to the instance's audience, on
// its host instead of a credential URI.
func WithActorJWTProviders(providers map[string]egress.ActorJWTProvider) WorkflowOption {
	return func(w *ActorWorkflow) { w.credentials.actorJWT = providers }
}

// WithDirectoryReleaser tells releaser about every deleted session that has a
// volume source. Without one, the owner of the volume learns of a deletion
// only by not finding the session.
func WithDirectoryReleaser(releaser DirectoryReleaser) WorkflowOption {
	return func(w *ActorWorkflow) { w.directories = releaser }
}

// WithToolsetTemplates lets a session with tool images move onto its agent's
// current revision: the toolset revision prepared from it for the session's
// tool images, its ActorTemplate created through templates. Without it such a
// session stays on its prepared revision.
func WithToolsetTemplates(templates toolsetTemplates) WorkflowOption {
	return func(w *ActorWorkflow) { w.toolsets = templates }
}

// quiescenceWake is a shared, single-slot hint channel. Callers must not close it;
// the workflow stops through context cancellation.
func NewActorWorkflow(store workflowStore, actors actorClient, quiescenceWake chan struct{}, recoveryInterval time.Duration, options ...WorkflowOption) *ActorWorkflow {
	workflow := &ActorWorkflow{store: store, actors: actors, recoveryInterval: recoveryInterval, quiescenceWake: quiescenceWake, settleDelay: time.Second, claimLease: 30 * time.Second,
		busyWait: 2 * time.Minute, busyRetry: 500 * time.Millisecond, crashWait: 15 * time.Second, crashRetry: time.Second}
	for _, option := range options {
		option(workflow)
	}
	return workflow
}

// Pause checkpoints the runtime on its current worker without changing the
// Session logical state or persisting A2A task state.
func (w *ActorWorkflow) Pause(ctx context.Context, session *apiv1alpha1.Session) error {
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	current, err := w.actors.GetActor(ctx, atespace, name)
	if err != nil {
		return err
	}
	if err := w.verifyActor(ctx, session, revision, current); err != nil {
		return err
	}
	actor, err := w.actors.PauseActor(ctx, atespace, name)
	if err != nil {
		return fmt.Errorf("pause Actor %s/%s: %w", atespace, name, err)
	}
	if !validActorIdentity(actor, revision, name) || actor.GetMetadata().GetUid() != current.GetMetadata().GetUid() || actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_PAUSED {
		return fmt.Errorf("pause Actor %s/%s returned status %s", atespace, name, actor.GetStatus().GetState())
	}
	return nil
}

// Quiesce durably suspends the runtime without changing the Session's
// logical READY state and records its external snapshot URI. Only a checkpoint
// retains a copy after the Actor advances.
func (w *ActorWorkflow) Quiesce(ctx context.Context, session *apiv1alpha1.Session) (*database.SessionTaskSnapshot, error) {
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return nil, fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	current, err := w.actors.GetActor(ctx, atespace, name)
	if err != nil {
		return nil, err
	}
	if err := w.verifyActor(ctx, session, revision, current); err != nil {
		return nil, err
	}
	actor, err := w.actors.SuspendActor(ctx, atespace, name)
	if err != nil {
		return nil, fmt.Errorf("suspend Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
		return nil, fmt.Errorf("suspend Actor %s/%s returned status %s", atespace, name, actor.GetStatus().GetState())
	}
	if !validActorIdentity(actor, revision, name) || actor.GetMetadata().GetUid() != current.GetMetadata().GetUid() {
		return nil, fmt.Errorf("suspend actor %s/%s returned invalid identity", atespace, name)
	}
	return externalSnapshot(actor)
}

// externalSnapshot is the session task snapshot a suspended Actor reports.
func externalSnapshot(actor *ateapipb.Actor) (*database.SessionTaskSnapshot, error) {
	atespace, name := actor.GetMetadata().GetAtespace(), actor.GetMetadata().GetName()
	snapshot := actor.GetStatus().GetExternalSnapshot()
	if snapshot.GetSnapshotUri() == "" {
		return nil, fmt.Errorf("suspend Actor %s/%s returned no snapshot", atespace, name)
	}
	scope := snapshot.GetContentScope()
	if scope != ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_FULL && scope != ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA {
		return nil, fmt.Errorf("actor %s/%s returned invalid snapshot content scope %s", atespace, name, scope)
	}
	return &database.SessionTaskSnapshot{
		Atespace: atespace, URI: snapshot.GetSnapshotUri(),
		ContentScope: strings.TrimPrefix(scope.String(), "SNAPSHOT_CONTENT_SCOPE_"),
	}, nil
}

// boundaryActor reads the session's Actor after a Pause or Quiesce whose
// outcome is unknown, with the external snapshot it holds when it is this
// session's Actor and suspended with a valid one. A missing Actor is returned
// as nil without an error: nothing will settle the boundary later.
func (w *ActorWorkflow) boundaryActor(ctx context.Context, session *apiv1alpha1.Session) (*ateapipb.Actor, *database.SessionTaskSnapshot, error) {
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return nil, nil, fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if status.Code(err) == codes.NotFound {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED || !validActorIdentity(actor, revision, name) {
		return actor, nil, nil
	}
	_, err = w.store.GetSessionForRuntime(ctx, session.GetId(), actor.GetMetadata().GetUid())
	if errors.Is(err, database.ErrNotFound) {
		return actor, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("verify runtime actor UID: %w", err)
	}
	snapshot, err := externalSnapshot(actor)
	if err != nil {
		return actor, nil, nil
	}
	return actor, snapshot, nil
}

// PauseNodeLost reports whether the session's Actor is paused on a checkpoint
// that only nodes without a worker still hold. Suspending such an Actor uploads
// the checkpoint through its node, which cannot complete, and Substrate leaves
// the Actor SUSPENDING; the pause is the node-loss handling's to crash, not a
// caller's to touch. A pause whose checkpoint has a durable copy, or that
// records no node, can be suspended from anywhere.
func (w *ActorWorkflow) PauseNodeLost(ctx context.Context, session *apiv1alpha1.Session) (bool, error) {
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return false, fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if err != nil {
		return false, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_PAUSED {
		return false, nil
	}
	local := actor.GetStatus().GetLocalSnapshot()
	node := local.GetNodeName()
	if local.GetDurableCopy().GetSnapshotUri() != "" || node == "" {
		return false, nil
	}
	workers, err := w.actors.ListAllWorkers(ctx)
	if err != nil {
		return false, fmt.Errorf("list workers: %w", err)
	}
	for _, worker := range workers {
		if worker.GetNodeName() == node && worker.GetStatus().GetState() != ateapipb.WorkerState_WORKER_STATE_DRAINING {
			return false, nil
		}
	}
	return true, nil
}

// ErrRuntimeLost reports a lifecycle operation that Substrate answered can
// never succeed: the session records the loss as FAILED and stays deletable.
var ErrRuntimeLost = errors.New("session runtime is lost")

// RuntimeLossError is ErrRuntimeLost with the cause the session records, as
// RuntimeLost names it, and the answer that settled it.
type RuntimeLossError struct {
	Cause string
	Err   error
}

func (e *RuntimeLossError) Error() string { return e.Cause + ": " + e.Err.Error() }

func (e *RuntimeLossError) Is(target error) bool { return target == ErrRuntimeLost }

func (e *RuntimeLossError) Unwrap() error { return e.Err }

// RuntimeLost reports whether the session's Actor crashed and whether its
// runtime can no longer take a turn. The runtime is lost when the Actor is
// gone, or Substrate reports it CRASHED without a durable snapshot to return it
// to, so every later message would fail the way the last one did, unless the
// session holds no task: its runtime kept nothing, and the resume reverts the
// Actor to the snapshot it started from (AwaitRuntime). A crashed
// Actor that holds an external snapshot is not lost: the resume returns it to
// that snapshot (AwaitRuntime), and the conversation goes on from there. The
// cause, empty while the Actor has not crashed, names the Actor and what became
// of it, with the crash's message when Substrate recorded one. An answer
// Substrate cannot give is returned as the error it is: not knowing is not the
// same as lost.
func (w *ActorWorkflow) RuntimeLost(ctx context.Context, session *apiv1alpha1.Session) (string, bool, error) {
	cause, lost, _, err := w.actorCrash(ctx, session)
	return cause, lost, err
}

// TurnCrash is RuntimeLost for a turn whose runtime stream broke: Substrate
// marks the Actor CRASHED only once it notices that the worker went away under
// the turn, so an Actor still RUNNING is asked about again, every crashRetry
// for at most crashWait. An Actor still RUNNING at that bound has not crashed.
func (w *ActorWorkflow) TurnCrash(ctx context.Context, session *apiv1alpha1.Session) (string, bool, error) {
	deadline := time.Now().Add(w.crashWait)
	for {
		cause, lost, state, err := w.actorCrash(ctx, session)
		if err != nil || state != ateapipb.ActorState_ACTOR_STATE_RUNNING || !time.Now().Before(deadline) {
			return cause, lost, err
		}
		select {
		case <-ctx.Done():
			return "", false, ctx.Err()
		case <-time.After(min(w.crashRetry, time.Until(deadline))):
		}
	}
}

// actorCrash is RuntimeLost with the state the Actor was found in.
func (w *ActorWorkflow) actorCrash(ctx context.Context, session *apiv1alpha1.Session) (string, bool, ateapipb.ActorState, error) {
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return "", false, ateapipb.ActorState_ACTOR_STATE_UNSPECIFIED, fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if status.Code(err) == codes.NotFound {
		return fmt.Sprintf("Actor %s/%s not found", atespace, name), true, ateapipb.ActorState_ACTOR_STATE_UNSPECIFIED, nil
	}
	if err != nil {
		return "", false, ateapipb.ActorState_ACTOR_STATE_UNSPECIFIED, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	state := actor.GetStatus().GetState()
	if state != ateapipb.ActorState_ACTOR_STATE_CRASHED {
		return "", false, state, nil
	}
	revertible, err := w.revertible(ctx, session, actor)
	if err != nil {
		return "", false, state, err
	}
	if !revertible {
		return fmt.Sprintf("Actor %s/%s crashed with no snapshot to return to", atespace, name), true, state, nil
	}
	cause := fmt.Sprintf("Actor %s/%s crashed", atespace, name)
	if message := actor.GetStatus().GetCrash().GetMessage(); message != "" {
		cause += ": " + message
	}
	return cause, false, state, nil
}

// revertible reports whether a revert returns the session's crashed Actor to
// a snapshot the conversation goes on from: the external snapshot it holds, the
// last one it suspended to or the template's golden it started from, or, for an
// Actor without one, the snapshot it started from when the session holds no
// task, so its runtime was given nothing to keep.
func (w *ActorWorkflow) revertible(ctx context.Context, session *apiv1alpha1.Session, actor *ateapipb.Actor) (bool, error) {
	if actor.GetStatus().GetExternalSnapshot().GetSnapshotUri() != "" {
		return true, nil
	}
	has, err := w.store.HasSessionTasks(ctx, session.GetId())
	if err != nil {
		return false, fmt.Errorf("check session tasks: %w", err)
	}
	return !has, nil
}

// RevertCrashed returns the session's crashed Actor to its snapshot when the
// conversation goes on from there (revertible), right after the turn the
// crash ended. Substrate's router keeps asking to resume a crashed Actor on
// the next send's dial until its parking budget is spent, and only then would
// the send's AwaitRuntime revert it; an Actor at rest is resumed on that dial
// at once. An Actor that has not crashed, or has nothing to return to, is left
// alone; a revert that fails is returned, and the next send's AwaitRuntime
// reverts the Actor then.
func (w *ActorWorkflow) RevertCrashed(ctx context.Context, session *apiv1alpha1.Session) error {
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if err != nil {
		return fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_CRASHED {
		return nil
	}
	revertible, err := w.revertible(ctx, session, actor)
	if err != nil || !revertible {
		return err
	}
	logging.FromContext(ctx).WarnContext(ctx, "the crashed Actor is reverted to its snapshot after the turn its crash ended",
		"session_id", session.GetId(), "actor", atespace+"/"+name, "crash", actor.GetStatus().GetCrash().GetMessage())
	if _, err := w.actors.RevertActor(ctx, atespace, name); err != nil {
		return fmt.Errorf("revert Actor %s/%s: %w", atespace, name, err)
	}
	return nil
}

// AwaitRuntime resumes the session's Actor, waiting while Substrate answers
// that it cannot yet (resumeLater): the worker pool rolls, another operation
// holds the Actor, such as a repoint onto a newer template, or Substrate is
// returning the crashed Actor to its snapshot, and each finishes on its own,
// after which the resume goes through. The wait is bounded by busyWait.
//
// Substrate refuses to resume a crashed Actor (FailedPrecondition, the Actor
// still crashed): a sandbox that died under its runtime, a restore whose
// runtime never came up. Such an Actor is reverted (RevertActor) and resumed
// once more when the revert returns it to a snapshot the conversation goes on
// from (revertible): the turn the crash ended is gone, the conversation is
// not. A second crash, or a crashed Actor with nothing to return to, is lost:
// the answer is a RuntimeLossError with the cause for the session to record.
// Any other answer, or an Actor Substrate still defers at the bound, is
// returned as the error it is.
func (w *ActorWorkflow) AwaitRuntime(ctx context.Context, session *apiv1alpha1.Session) error {
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	ctx, cancel := context.WithTimeout(ctx, w.busyWait)
	defer cancel()
	for reverted := false; ; reverted = true {
		err = w.awaitResume(ctx, func() error {
			_, err := w.actors.ResumeActor(ctx, atespace, name)
			return err
		})
		if err == nil {
			return nil
		}
		err = fmt.Errorf("resume Actor %s/%s: %w", atespace, name, err)
		if status.Code(err) != codes.FailedPrecondition {
			return err
		}
		actor, getErr := w.actors.GetActor(ctx, atespace, name)
		if getErr != nil || actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_CRASHED {
			return err
		}
		crash := actor.GetStatus().GetCrash().GetMessage()
		if !reverted {
			revertible, taskErr := w.revertible(ctx, session, actor)
			if taskErr != nil {
				return errors.Join(err, taskErr)
			}
			if revertible {
				logging.FromContext(ctx).WarnContext(ctx, "the crashed Actor is reverted to its snapshot and resumed again",
					"session_id", session.GetId(), "actor", atespace+"/"+name, "crash", crash)
				if _, revertErr := w.actors.RevertActor(ctx, atespace, name); revertErr != nil {
					return errors.Join(err, fmt.Errorf("revert Actor %s/%s: %w", atespace, name, revertErr))
				}
				continue
			}
		}
		return &RuntimeLossError{
			Cause: fmt.Sprintf("Actor %s/%s crashed and cannot be resumed", atespace, name),
			Err:   fmt.Errorf("%w; crash: %s", err, crash),
		}
	}
}

// Create provisions the persisted session once, using its pinned checkpoint for
// forks. Retries return current state; an uncertain prior creation blocks execution.
func (w *ActorWorkflow) Create(ctx context.Context, session *apiv1alpha1.Session) (*apiv1alpha1.Session, error) {
	return w.run(ctx, session.GetId(), apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_CREATE)
}

// Suspend returns after the Actor and session are suspended. A retry observes
// the same operation rather than issuing a second mutation.
func (w *ActorWorkflow) Suspend(ctx context.Context, session *apiv1alpha1.Session) (*apiv1alpha1.Session, error) {
	return w.run(ctx, session.GetId(), apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_SUSPEND)
}

// Resume returns after the Actor is running and the session is ready. Missing
// Actors are errors; Resume never creates replacement compute.
func (w *ActorWorkflow) Resume(ctx context.Context, session *apiv1alpha1.Session) (*apiv1alpha1.Session, error) {
	return w.run(ctx, session.GetId(), apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_RESUME)
}

// Delete closes admission before stopping and deleting compute. It can supersede
// unissued creation, but never deletes a session while a prior call is uncertain.
func (w *ActorWorkflow) Delete(ctx context.Context, session *apiv1alpha1.Session) (*apiv1alpha1.Session, error) {
	return w.run(ctx, session.GetId(), apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_DELETE)
}

func (w *ActorWorkflow) run(ctx context.Context, sessionID string, requestedKind apiv1alpha1.RuntimeOperation) (*apiv1alpha1.Session, error) {
	ctx, cancelAttempt := context.WithTimeout(ctx, database.RuntimeOperationTimeout)
	defer cancelAttempt()
	operation, err := w.store.BeginSessionOperation(ctx, sessionID, requestedKind)
	if errors.Is(err, database.ErrConflict) && requestedKind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_DELETE && w.endHeldResume(ctx, sessionID) {
		operation, err = w.store.BeginSessionOperation(ctx, sessionID, requestedKind)
	}
	if err != nil {
		return nil, err
	}
	result, err := w.execute(ctx, operation)
	if err == nil && result.GetState() == apiv1alpha1.RuntimeState_RUNTIME_STATE_READY {
		// A resume can make old unclaimed idle work eligible again.
		w.wakeQuiescence()
	}
	return result, err
}

// endHeldResume ends the session's resume that an earlier attempt left
// pending and no attempt runs now, when its Actor rests suspended where the
// operation found it (resumeUnapplied), and reports whether it did. A resume
// Substrate failed for a transient cause, such as a worker drained under the
// restore, crashes the Actor only until Substrate returns it to rest; its
// attempt finds the Actor crashed and keeps the operation, which would refuse
// the session's delete until another resume settled it.
func (w *ActorWorkflow) endHeldResume(ctx context.Context, sessionID string) bool {
	held, err := w.store.GetCurrentSessionOperation(ctx, sessionID)
	if err != nil || held.Instance.Operation != apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_RESUME {
		return false
	}
	revision, err := w.store.GetRuntimeRevision(ctx, held.Instance.GetPreparedRevision())
	if err != nil {
		return false
	}
	executorID := uuid.New()
	claimed, err := w.store.ClaimSessionOperation(ctx, sessionID, held.ID, executorID)
	if err != nil || !claimed {
		return false
	}
	if w.resumeUnapplied(ctx, sessionID, revision) {
		if _, err := w.store.CancelSessionOperation(ctx, sessionID, held.ID, executorID); err == nil {
			return true
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := w.store.ReleaseRuntimeOperation(finishCtx, sessionID, held.ID, executorID); err != nil {
		logging.FromContext(ctx).WarnContext(ctx, "the delete's claim on a held resume was not released; it expires",
			"session_id", sessionID, "operation_id", held.ID, "error", err)
	}
	return false
}

// execute keeps lifecycle preparation separate from the durable issue boundary.
// Multiple callers may prepare using read-only calls; exactly one can authorize
// runtime mutations for a bounded attempt. Errors retain the operation and its
// resource pins so a client retry can continue it, except a failed resume whose
// Actor is found still suspended (resumeUnapplied): its outcome is known, so the
// operation ends and the session takes any next operation. Session admission still
// serializes lifecycle against runtime writes, idle work, and checkpoints.
func (w *ActorWorkflow) execute(ctx context.Context, operation *database.SessionOperation) (_ *apiv1alpha1.Session, err error) {
	sessionID := operation.Instance.Id
	if operation.Instance.Operation == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_NONE {
		return operationOutcome(operation)
	}
	kind := operation.Instance.Operation
	session := operation.Instance
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return w.failPreparation(ctx, operation, fmt.Errorf("load prepared revision: %w", err))
	}
	var snapshot *database.SessionTaskSnapshot
	var tagName string
	if kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_CREATE && operation.SourceCheckpointID != nil {
		snapshot, _, err = w.store.GetSessionCheckpointSnapshot(ctx, operation.SourceCheckpointID.String(), session.Creator)
		if err != nil {
			return w.failPreparation(ctx, operation, fmt.Errorf("load pinned checkpoint: %w", err))
		}
		if snapshot == nil || snapshot.URI == "" || snapshot.Atespace == "" || snapshot.ContentScope != "DATA" {
			return w.failPreparation(ctx, operation, fmt.Errorf("fork requires a retained DATA checkpoint"))
		}
		tagName = "checkpoint-" + operation.SourceCheckpointID.String()
	}

	binding := actorBinding(session.Id, revision)
	var creation *substrate.ActorCreation
	if kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_CREATE {
		policy, err := w.sessionPolicy(ctx, binding.Atespace, revision, session.GetProviders(), betweenTurns)
		if err != nil {
			return w.failPreparation(ctx, operation, err)
		}
		existing, err := substrate.SessionExistingVolumes(session.Id, session.GetVolumeSource())
		if err != nil {
			return w.failPreparation(ctx, operation, err)
		}
		creation = &substrate.ActorCreation{EgressPolicy: policy, ExistingVolumes: existing}
		if snapshot != nil {
			creation.Snapshot = &substrate.ActorSnapshot{Tag: &ateapipb.ObjectRef{Atespace: snapshot.Atespace, Name: tagName}, URI: snapshot.URI, ContentScope: ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA}
		}
	}
	// A suspended Actor of a superseded revision resumes on the agent's current
	// one (see repointTarget). An earlier attempt of this operation may have
	// moved the Actor before its record was written: it is then found on the
	// current template and the record follows below.
	var current *database.RuntimeRevision
	var currentPolicy *ateapipb.EgressPolicy
	if kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_RESUME {
		current, currentPolicy, err = w.repointTarget(ctx, session, revision)
		if err != nil {
			return w.failPreparation(ctx, operation, err)
		}
	}
	if current != nil {
		var actor *ateapipb.Actor
		err := w.untilSettled(ctx, kind, func() (err error) {
			actor, err = w.actors.GetActor(ctx, binding.Atespace, binding.Name)
			return err
		})
		if err != nil {
			return w.failPreparation(ctx, operation, fmt.Errorf("get Actor %s/%s: %w", binding.Atespace, binding.Name, err))
		}
		switch {
		case validActorIdentity(actor, current, binding.Name):
			binding = actorBinding(session.Id, current)
		case actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED:
			current = nil
		}
	}
	prepare := substrate.PrepareActorTransition
	if operation.ExecutorID != uuid.Nil {
		prepare = substrate.PrepareActorRetry
	}
	var transition *substrate.ActorTransition
	err = w.untilSettled(ctx, kind, func() (err error) {
		transition, err = prepare(ctx, w.actors, binding, kind, creation)
		return err
	})
	if err != nil {
		return w.failPreparation(ctx, operation, err)
	}

	if uid := transition.ActorUID(); uid != "" && kind != apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_CREATE {
		if _, err := w.store.GetSessionForRuntime(ctx, session.Id, uid); err != nil {
			return w.failPreparation(ctx, operation, fmt.Errorf("verify runtime actor UID: %w", err))
		}
	}

	executorID := uuid.New()
	claimed, err := w.store.ClaimSessionOperation(ctx, sessionID, operation.ID, executorID)
	if err != nil {
		return nil, err
	}
	if !claimed {
		// A superseded generation returns a conflict without runtime work.
		current, err := w.store.GetSessionOperation(ctx, sessionID, operation.ID)
		if err != nil {
			return nil, err
		}
		return operationOutcome(current)
	}
	// recorded is the revision the session records for this operation; the
	// Actor of an unapplied resume is found suspended on its template.
	recorded := revision
	defer func() {
		if err == nil || errors.Is(err, ErrRuntimeLost) {
			return // Completion or the recorded failure already cleared the claim.
		}
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_RESUME && w.resumeUnapplied(finishCtx, sessionID, recorded) {
			_, cancelErr := w.store.CancelSessionOperation(finishCtx, sessionID, operation.ID, executorID)
			if cancelErr == nil {
				err = fmt.Errorf("lifecycle operation %s ended, the Actor is still suspended: %w", operation.ID, err)
				return
			}
			logging.FromContext(ctx).WarnContext(ctx, "the failed resume keeps its operation: it could not be ended",
				"session_id", sessionID, "operation_id", operation.ID, "error", cancelErr)
		}
		err = errors.Join(fmt.Errorf("lifecycle operation %s remains pending: %w", operation.ID, err),
			w.store.ReleaseRuntimeOperation(finishCtx, sessionID, operation.ID, executorID))
	}()
	if current != nil {
		var repointed *ateapipb.Actor
		err := w.untilSettled(ctx, kind, func() (err error) {
			repointed, err = w.repointActor(ctx, session.Id, current, currentPolicy)
			return err
		})
		if err != nil {
			return nil, err
		}
		if repointed != nil {
			if err := w.recordResumeRepoint(ctx, session, operation.ID, executorID, current); err != nil {
				return nil, err
			}
			recorded = current
			if moved := actorBinding(session.Id, current); moved != binding {
				binding = moved
				err = w.untilSettled(ctx, kind, func() (err error) {
					transition, err = substrate.PrepareActorRetry(ctx, w.actors, binding, kind, creation)
					return err
				})
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_RESUME {
		// The suspended Actor resumes on the grants its source holds now, should
		// the controller's application not have reached it yet.
		if err := w.untilSettled(ctx, kind, func() error { return w.applySessionEgress(ctx, session, recorded, binding) }); err != nil {
			return nil, err
		}
	}
	if err := w.untilSettled(ctx, kind, func() error { return substrate.ApplyActorTransition(ctx, w.actors, transition) }); err != nil {
		if kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_RESUME && status.Code(err) == codes.DataLoss {
			// Substrate answers DataLoss when the Actor's snapshot cannot be
			// restored, its own or the golden one it builds on: no retry can
			// resume it, so the outcome is known and the operation must not stay
			// pending, which would refuse every later resume and delete.
			message := fmt.Sprintf("%sresume Actor %s/%s: %v", apia2a.RuntimeLostMessagePrefix, binding.Atespace, binding.Name, status.Convert(err).Message())
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			failed, failErr := w.store.FailSessionOperation(finishCtx, sessionID, operation.ID, executorID,
				&apiv1alpha1.Failure{Reason: apia2a.FailureReasonRuntimeLost, Message: message})
			if failErr != nil {
				return nil, errors.Join(fmt.Errorf("resume Actor %s/%s: %w", binding.Atespace, binding.Name, err), failErr)
			}
			return failed, fmt.Errorf("%s: %w", message, ErrRuntimeLost)
		}
		return nil, err
	}
	var authority string
	if kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_CREATE {
		// The controller skips a creation it finds before its Actor exists, so
		// the new Actor takes the grants its source holds after it was created.
		if err := w.applySessionEgress(ctx, session, recorded, binding); err != nil {
			return nil, err
		}
		authority = substrate.ActorHost(binding.Atespace, binding.Name, "")
	}

	// A disconnected client must not discard an already known runtime outcome.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	finished, err := w.store.FinishSessionOperation(finishCtx, sessionID, operation.ID, executorID, authority, transition.ActorUID(), "")
	if err == nil && kind == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_DELETE {
		w.releaseDirectory(finishCtx, session)
	}
	return finished, err
}

// untilSettled runs call, one of the operation's ate-api calls, once; a
// resume's call Substrate answers later (resumeLater) is issued again until
// it goes through or the attempt ends. The worker pool or the control plane
// rolling under a session is not the session's failure, and the attempt has
// the time to wait the roll out: while ate-api's pods are replaced, the
// controller's connection answers every call Unavailable until it reaches a
// new one, the reads and the repoint before the resume as much as the resume.
func (w *ActorWorkflow) untilSettled(ctx context.Context, kind apiv1alpha1.RuntimeOperation, call func() error) error {
	if kind != apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_RESUME {
		return call()
	}
	return w.awaitResume(ctx, call)
}

// resumeUnapplied reports whether a failed resume left the session's Actor as
// the operation found it: suspended, the session's own, on the template of the
// revision the session records, or gone, so that no resume can take effect.
// The outcome is then known and the operation can end; holding it would refuse
// the session's delete until another resume. An Actor that cannot be read, is
// no longer suspended or has moved keeps the operation for the next attempt to
// settle.
func (w *ActorWorkflow) resumeUnapplied(ctx context.Context, sessionID string, revision *database.RuntimeRevision) bool {
	name := substrate.ActorName(sessionID)
	var actor *ateapipb.Actor
	err := w.awaitResume(ctx, func() (err error) {
		actor, err = w.actors.GetActor(ctx, revision.ActorTemplateAtespace, name)
		return err
	})
	if status.Code(err) == codes.NotFound {
		return true
	}
	if err != nil || actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED || !validActorIdentity(actor, revision, name) {
		return false
	}
	_, err = w.store.GetSessionForRuntime(ctx, sessionID, actor.GetMetadata().GetUid())
	return err == nil
}

// releaseDirectory tells the owner of a deleted session's volume that its
// directory may go. The session is deleted whatever the outcome, so a failure
// is logged: the owner also cleans up a directory whose session it no longer
// finds.
func (w *ActorWorkflow) releaseDirectory(ctx context.Context, session *apiv1alpha1.Session) {
	if w.directories == nil || session.GetVolumeSource() == nil {
		return
	}
	if err := w.directories.ReleaseSessionDirectory(ctx, session); err != nil {
		logging.FromContext(ctx).ErrorContext(ctx, "the owner of the deleted session's volume was not told its directory may go",
			"session_id", session.GetId(), "error", err)
	}
}

// actorBinding names the session's Actor and the ActorTemplate of revision.
func actorBinding(sessionID string, revision *database.RuntimeRevision) substrate.ActorBinding {
	return substrate.ActorBinding{Atespace: revision.ActorTemplateAtespace, Name: substrate.ActorName(sessionID),
		TemplateAtespace: revision.ActorTemplateAtespace, TemplateName: revision.ActorTemplateName}
}

// repointTarget returns the revision an Actor of revision moves to when it
// resumes, with that revision's egress allowlist plus the session's admitted
// providers, or nil when it stays: a
// revision rendered by an older release lacks configuration the platform needs
// now, and once no session references it the runtime revision GC collects it.
// The target is the agent's current revision in the same atespace; an Actor
// never moves between atespaces.
func (w *ActorWorkflow) repointTarget(ctx context.Context, session *apiv1alpha1.Session, revision *database.RuntimeRevision) (*database.RuntimeRevision, *ateapipb.EgressPolicy, error) {
	current, err := w.store.GetCurrentRuntimeRevision(ctx, revision.Revision)
	switch {
	case errors.Is(err, database.ErrNotFound):
		return nil, nil, nil
	case err != nil:
		return nil, nil, fmt.Errorf("load current revision: %w", err)
	case current.Revision == revision.Revision || current.ActorTemplateAtespace != revision.ActorTemplateAtespace:
		return nil, nil, nil
	}
	// The grants a revision's actors hold are its Agent's: a toolset revision,
	// recorded only with the move, holds its base's.
	grants := current.Revision
	if len(session.GetToolImages()) > 0 {
		if current = w.toolsetRepointTarget(ctx, session, revision, current); current == nil {
			return nil, nil, nil
		}
	}
	live, err := w.liveRevisionWith(ctx, current, grants)
	if err != nil {
		return nil, nil, fmt.Errorf("build egress policy of revision %s: %w", current.Revision, err)
	}
	policy, err := sessionEgressPolicy(current.ActorTemplateAtespace, live, w.credentials, session.GetProviders(), betweenTurns)
	if err != nil {
		return nil, nil, fmt.Errorf("build egress policy of revision %s: %w", current.Revision, err)
	}
	return current, policy, nil
}

// toolsetRepointTarget returns the revision a session with tool images moves
// to from revision: its agent's current revision prepared with the session's
// tool images, recorded with the move. Its ActorTemplate is created by the
// first caller and taken once its golden snapshot is ready; until then, and
// for a session already on it, the session stays, as it does when the
// toolset cannot be prepared.
func (w *ActorWorkflow) toolsetRepointTarget(ctx context.Context, session *apiv1alpha1.Session, revision, current *database.RuntimeRevision) *database.RuntimeRevision {
	if w.toolsets == nil {
		return nil
	}
	log := logging.FromContext(ctx).With("session_id", session.GetId(), "revision", current.Revision)
	toolset, err := toolsetOf(session.GetToolImages())
	if err != nil {
		log.WarnContext(ctx, "the session stays on its prepared revision: its tool images do not parse", "error", err)
		return nil
	}
	target, desired, err := toolsetRevision(ctx, w.toolsets, current, toolset)
	if err != nil {
		log.WarnContext(ctx, "the session stays on its prepared revision: its toolset revision could not be derived", "error", err)
		return nil
	}
	if target.Revision == revision.Revision {
		return nil
	}
	observed, err := ensureToolsetTemplate(ctx, w.toolsets, desired, 0)
	if err != nil {
		log.WarnContext(ctx, "the session stays on its prepared revision: its toolset revision could not be prepared", "toolset_revision", target.Revision, "error", err)
		return nil
	}
	if observed == nil {
		log.InfoContext(ctx, "the session stays on its prepared revision until its toolset revision is prepared", "toolset_revision", target.Revision)
		return nil
	}
	target.ActorTemplateUID = observed.GetMetadata().GetUid()
	return target
}

// recordResumeRepoint records that the claiming executor of a resume moved the
// session's Actor onto current, recording current with the move when it is a
// toolset revision.
func (w *ActorWorkflow) recordResumeRepoint(ctx context.Context, session *apiv1alpha1.Session, operationID, executorID uuid.UUID, current *database.RuntimeRevision) error {
	if len(session.GetToolImages()) > 0 {
		return w.store.RepointToolsetSessionOperation(ctx, session.GetId(), operationID, executorID, *current)
	}
	return w.store.RepointSessionOperation(ctx, session.GetId(), operationID, executorID, current.Revision)
}

// sessionPolicy compiles the egress allowlist of a session's Actor on revision
// in phase (sessionEgressPolicy), with the access the revision's actors hold
// now (liveRevision).
func (w *ActorWorkflow) sessionPolicy(ctx context.Context, atespace string, revision *database.RuntimeRevision, providers []*apiv1alpha1.SessionProvider, phase turnPhase) (*ateapipb.EgressPolicy, error) {
	live, err := w.liveRevision(ctx, revision)
	if err != nil {
		return nil, err
	}
	return sessionEgressPolicy(atespace, live, w.credentials, providers, phase)
}

// liveRevision is revision with the access its actors hold now: its pinned
// base plus the EgressPolicy grants its Agent holds, those that fit the base
// (egress.EffectiveAccess). A deleted or recreated Agent grants nothing.
func (w *ActorWorkflow) liveRevision(ctx context.Context, revision *database.RuntimeRevision) (*database.RuntimeRevision, error) {
	return w.liveRevisionWith(ctx, revision, revision.Revision)
}

// liveRevisionWith is liveRevision with the grants the actors of the recorded
// revision grantsOf hold, a revision of the same Agent.
func (w *ActorWorkflow) liveRevisionWith(ctx context.Context, revision *database.RuntimeRevision, grantsOf string) (*database.RuntimeRevision, error) {
	grants, err := w.store.GetRuntimeEgressGrant(ctx, grantsOf)
	if err != nil {
		return nil, fmt.Errorf("load the egress grants of revision %s: %w", grantsOf, err)
	}
	access, err := egress.EffectiveAccess(egress.Access{Destinations: revision.EgressDestinations, Credentials: revision.Credentials}, grants, revision.CallerCredential)
	if err != nil {
		return nil, fmt.Errorf("the egress grants of revision %s: %w", revision.Revision, err)
	}
	live := *revision
	live.EgressDestinations, live.Credentials = access.Destinations, access.Credentials
	return &live, nil
}

// ApplyEgress gives the Actor of a session the egress policy of its revision
// with the grants its Agent holds now, the controller's application of an
// EgressPolicy change to a running or suspended Actor. A session that is gone,
// whose Actor is gone, or whose Actor is not the one target names, has nothing
// to change: a creation and a repoint write the current policy themselves.
func (w *ActorWorkflow) ApplyEgress(ctx context.Context, target database.RuntimeEgressTarget) error {
	session, err := w.store.GetSessionByID(ctx, target.ID)
	if errors.Is(err, database.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if errors.Is(err, database.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	binding := actorBinding(session.GetId(), revision)
	actor, err := w.actors.GetActor(ctx, binding.Atespace, binding.Name)
	if status.Code(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if !validActorIdentity(actor, revision, binding.Name) || (target.ActorUID != "" && actor.GetMetadata().GetUid() != target.ActorUID) {
		return nil
	}
	return w.applySessionEgress(ctx, session, revision, binding)
}

// applySessionEgress gives the session's Actor on revision the policy of its
// phase with the grants its Agent holds now: during a turn, which the Actor's
// rules on its actor-JWT hosts show, the turn's; between turns the other. A
// turn opening or closing meanwhile moves the policy's version, and the
// application reads the phase again.
func (w *ActorWorkflow) applySessionEgress(ctx context.Context, session *apiv1alpha1.Session, revision *database.RuntimeRevision, binding substrate.ActorBinding) error {
	closed := w.credentials.actorJWTHosts(session.GetProviders())
	err := w.actors.ApplyActorEgressPolicy(ctx, binding.Atespace, binding.Name, func(existing *ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error) {
		phase := betweenTurns
		if opensHost(existing, closed) {
			phase = duringTurn
		}
		return w.sessionPolicy(ctx, binding.Atespace, revision, session.GetProviders(), phase)
	})
	if err != nil {
		return fmt.Errorf("apply the egress grants of Actor %s/%s: %w", binding.Atespace, binding.Name, err)
	}
	return nil
}

// opensHost reports whether policy has a rule on one of hosts.
func opensHost(policy *ateapipb.EgressPolicy, hosts map[string]bool) bool {
	for _, rule := range policy.GetRules() {
		for _, names := range [][]string{rule.GetHttps().GetHostnames(), rule.GetHttp().GetHostnames(), rule.GetTlsPassthrough().GetHostnames()} {
			for _, host := range names {
				if hosts[strings.ToLower(host)] {
					return true
				}
			}
		}
	}
	return false
}

// turnPhase is when an egress policy of a session's Actor applies.
type turnPhase int

const (
	// betweenTurns is every policy written outside a turn: on create, on a
	// repoint and when a turn ends.
	betweenTurns turnPhase = iota
	// duringTurn is the policy a turn's dispatch opens.
	duringTurn
)

// sessionEgressPolicy compiles the egress allowlist of a session's Actor on
// revision. A binding scoped to the golden boot, such as a private skill
// source's git credential, stays off it: the sandbox reaches that host
// without the header. The hosts the session was admitted with for its volume
// source (its providers) are allowed over HTTPS on this Actor alone, so the
// session's directory setup and its git and API calls reach them without the
// Agent listing them, and each gets its caller credential there
// (providerCredentials); the revision and the Agent's other sessions are
// unchanged. A provider never takes a header the Agent binds on its host, on
// no path of it: the person's token and the Agent's own credential never meet
// on one host.
//
// Between turns the hosts of the providers that verify the Actor's own JWT
// are off the policy, every origin of theirs the Agent lists too: the
// session's runtime reaches them only with that JWT, which the gateway hands
// out only during a turn (OpenTurnEgress). A process left running in the
// sandbox between turns is refused there at the egress gateway.
func sessionEgressPolicy(atespace string, revision *database.RuntimeRevision, authority providerAuthority, providers []*apiv1alpha1.SessionProvider, phase turnPhase) (*ateapipb.EgressPolicy, error) {
	destinations := slices.Clone(revision.EgressDestinations)
	agent := egress.ForActors(revision.Credentials)
	if phase == betweenTurns {
		closed := authority.actorJWTHosts(providers)
		providers = slices.DeleteFunc(slices.Clone(providers), func(provider *apiv1alpha1.SessionProvider) bool {
			return closed[strings.ToLower(provider.GetHostname())]
		})
		destinations = slices.DeleteFunc(destinations, func(destination string) bool {
			u, err := url.Parse(destination)
			return err == nil && closed[strings.ToLower(u.Hostname())]
		})
		agent = slices.DeleteFunc(agent, func(c egress.Credential) bool { return closed[strings.ToLower(c.Hostname)] })
	}
	// An installation credential-URI provider is on every bound Session,
	// whatever its agent runs, so it opens no host and binds only where the
	// revision declares the header for the Session's person on its host and
	// path (egress.Credential FromSession): a codex agent on the person's own
	// ChatGPT login. An agent that merely reaches the host gets nothing.
	declared, agent := egress.SessionDeclared(agent)
	providers = slices.DeleteFunc(slices.Clone(providers), func(provider *apiv1alpha1.SessionProvider) bool {
		if !installationCredential(authority.actorJWT, provider) {
			return false
		}
		return !slices.ContainsFunc(declared, func(c egress.Credential) bool {
			return c.Hostname == strings.ToLower(provider.GetHostname()) && c.Header == "authorization" &&
				c.Prefix == provider.GetScheme()+" " && c.PathPrefix == provider.GetPathPrefix()
		})
	})
	for _, provider := range providers {
		if !installationCredential(authority.actorJWT, provider) {
			destinations = append(destinations, "https://"+provider.GetHostname())
		}
	}
	callers, err := egress.CanonicalProviderCredentials(providerCredentials(authority, providers))
	if err != nil {
		return nil, fmt.Errorf("the session's provider credentials: %w", err)
	}
	for _, caller := range callers {
		if slices.ContainsFunc(agent, func(c egress.Credential) bool { return c.Hostname == caller.Hostname && c.Header == caller.Header }) {
			return nil, fmt.Errorf("the session's provider credentials: conflicting credentials for %s header %s", caller.Hostname, caller.Header)
		}
	}
	credentials, err := egress.MergeCredentials(agent, callers)
	if err != nil {
		return nil, fmt.Errorf("the session's provider credentials: %w", err)
	}
	return substrate.ActorEgressPolicy(atespace, destinations, credentials)
}

// providerAuthority is how an installation resolves a session's provider
// credentials: through the credential provider at authority, or, for the
// provider instances of actorJWT, with the Actor's own JWT bound to the
// instance's audience, which that provider verifies itself.
type providerAuthority struct {
	authority string
	actorJWT  map[string]egress.ActorJWTProvider
}

// providerCredentials binds each provider's credential on its host: the
// egress gateway replaces the Authorization header a request from the
// session's Actor carries with the credential it fetches for the provider's
// URI at authority (egress.ProviderCredentialURI), sending the Actor's
// verified identity. The credential provider finds the session's person from
// that Actor and answers the git credential already encoded for Basic and the
// raw token for Bearer, so the rule prepends only the scheme. The token never
// reaches the sandbox or the controller. A provider with a path prefix binds
// the request paths under it alone (GitLab's Bearer API under "/api/" beside
// its Basic git on the rest of the host). A Bearer provider whose instance
// verifies the Actor's JWT (muster, which serves the session's person to its
// own sandbox) gets that JWT, minted per request for the instance's audience,
// and its URI is never resolved. Without an authority no other provider is
// bound. A provider stored before its instance had a field of its own carries
// the instance as its audience.
func providerCredentials(resolver providerAuthority, providers []*apiv1alpha1.SessionProvider) []egress.Credential {
	var bindings []egress.Credential
	for _, provider := range providers {
		//nolint:staticcheck // audience is the instance of a session stored before provider existed.
		instance := cmp.Or(provider.GetProvider(), provider.GetAudience())
		binding := egress.Credential{Hostname: provider.GetHostname(), Header: "authorization", Prefix: provider.GetScheme() + " ", PathPrefix: provider.GetPathPrefix()}
		if verifier, verifies := actorJWTVerifier(resolver.actorJWT, provider); verifies {
			binding.Audience = verifier.Audience
		} else if resolver.authority != "" {
			binding.URI = egress.ProviderCredentialURI(resolver.authority, instance, provider.GetScheme())
		} else {
			continue
		}
		bindings = append(bindings, binding)
	}
	return bindings
}

// actorJWTHosts are the hosts, in lower case, of the providers whose
// instance verifies the Actor's own JWT.
func (a providerAuthority) actorJWTHosts(providers []*apiv1alpha1.SessionProvider) map[string]bool {
	hosts := map[string]bool{}
	for _, provider := range providers {
		if _, verifies := actorJWTVerifier(a.actorJWT, provider); verifies {
			hosts[strings.ToLower(provider.GetHostname())] = true
		}
	}
	return hosts
}

// actorJWTVerifier returns the instance of actorJWT that verifies the Actor's
// own JWT on provider's host: a Bearer provider naming it.
func actorJWTVerifier(actorJWT map[string]egress.ActorJWTProvider, provider *apiv1alpha1.SessionProvider) (egress.ActorJWTProvider, bool) {
	//nolint:staticcheck // audience is the instance of a session stored before provider existed.
	verifier, verifies := actorJWT[cmp.Or(provider.GetProvider(), provider.GetAudience())]
	return verifier, verifies && verifier.VerifiesActorJWT() && provider.GetScheme() == "Bearer"
}

// installationCredential reports whether provider is one the installation
// admits every bound Session with and the egress gateway resolves by
// credential URI (an instance of actorJWT without an audience). A grant never
// names such an instance, so the instance alone identifies it.
func installationCredential(actorJWT map[string]egress.ActorJWTProvider, provider *apiv1alpha1.SessionProvider) bool {
	//nolint:staticcheck // audience is the instance of a session stored before provider existed.
	configured, ok := actorJWT[cmp.Or(provider.GetProvider(), provider.GetAudience())]
	return ok && !configured.VerifiesActorJWT()
}

// turnEgress is the session's Actor and the egress policy it holds during a
// turn and between turns; nil when the two are the same, for a session
// without a provider that verifies the Actor's JWT.
func (w *ActorWorkflow) turnEgress(ctx context.Context, session *apiv1alpha1.Session) (binding substrate.ActorBinding, during, between *ateapipb.EgressPolicy, err error) {
	if len(w.credentials.actorJWTHosts(session.GetProviders())) == 0 {
		return binding, nil, nil, nil
	}
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return binding, nil, nil, fmt.Errorf("load prepared revision: %w", err)
	}
	binding = actorBinding(session.GetId(), revision)
	if during, err = w.sessionPolicy(ctx, binding.Atespace, revision, session.GetProviders(), duringTurn); err != nil {
		return binding, nil, nil, err
	}
	if between, err = w.sessionPolicy(ctx, binding.Atespace, revision, session.GetProviders(), betweenTurns); err != nil {
		return binding, nil, nil, err
	}
	return binding, during, between, nil
}

// OpenTurnEgress gives the session's Actor the rules of its actor-JWT
// providers for the turn the caller is about to dispatch, so the egress
// gateway hands the Actor its JWT on their hosts until the turn's end closes
// them, and returns the policy written: the fence that closes them again if
// the turn never starts (CloseTurnEgress). The caller holds the session's
// dispatch: no idle work or lifecycle operation writes the policy meanwhile.
// The write always lands, so a close fenced behind an earlier read loses to
// it. A session whose policy no turn changes writes nothing and gets no fence.
func (w *ActorWorkflow) OpenTurnEgress(ctx context.Context, session *apiv1alpha1.Session) (*ateapipb.EgressPolicy, error) {
	binding, during, _, err := w.turnEgress(ctx, session)
	if err != nil || during == nil {
		return nil, err
	}
	written, err := w.actors.WriteActorEgressPolicy(ctx, binding.Atespace, binding.Name, during)
	if err != nil {
		return nil, fmt.Errorf("open the turn's egress of Actor %s/%s: %w", binding.Atespace, binding.Name, err)
	}
	return written, nil
}

// TurnEgressFence reads the session's Actor egress policy while its turn
// still holds the session, before the step that ends the turn: the fence
// CloseTurnEgress writes behind. Nil for a session whose policy no turn
// changes.
func (w *ActorWorkflow) TurnEgressFence(ctx context.Context, session *apiv1alpha1.Session) (*ateapipb.EgressPolicy, error) {
	binding, during, _, err := w.turnEgress(ctx, session)
	if err != nil || during == nil {
		return nil, err
	}
	fence, err := w.actors.ReadActorEgressPolicy(ctx, binding.Atespace, binding.Name)
	if err != nil {
		return nil, fmt.Errorf("read the turn's egress of Actor %s/%s: %w", binding.Atespace, binding.Name, err)
	}
	return fence, nil
}

// CloseTurnEgress takes the rules of the session's actor-JWT providers off
// its Actor once its turn ended, unless the policy changed since fence was
// read: the next turn has opened it, and its own end closes it. An Actor that
// is gone has nothing to close.
func (w *ActorWorkflow) CloseTurnEgress(ctx context.Context, session *apiv1alpha1.Session, fence *ateapipb.EgressPolicy) error {
	if fence == nil {
		return nil
	}
	binding, _, between, err := w.turnEgress(ctx, session)
	if err != nil || between == nil || substrate.SameRules(fence, between) {
		return err
	}
	_, err = w.actors.UpdateActorEgressPolicyAt(ctx, binding.Atespace, binding.Name, fence, between)
	switch status.Code(err) {
	case codes.OK, codes.Aborted, codes.NotFound:
		return nil
	}
	return fmt.Errorf("close the turn's egress of Actor %s/%s: %w", binding.Atespace, binding.Name, err)
}

// closeEndedTurnEgress closes the actor-JWT hosts of a session no turn can
// open meanwhile, as its idle work's claim keeps every turn off it.
func (w *ActorWorkflow) closeEndedTurnEgress(ctx context.Context, session *apiv1alpha1.Session) error {
	fence, err := w.TurnEgressFence(ctx, session)
	if err != nil {
		return err
	}
	return w.CloseTurnEgress(ctx, session, fence)
}

// ActorJWTSessionProviders are the Session providers of the installation's
// instances, sorted by instance: one on each instance's host and path prefix,
// Bearer for one that verifies the Actor's own JWT, the configured scheme for
// one resolved by credential URI. An admitted Session gets them, and so does
// one without an admission grant whose creator binds its Actor, so the egress
// gateway hands its Actor that JWT, or the credential the provider answers for
// the Session's person, on those hosts.
func ActorJWTSessionProviders(actorJWT map[string]egress.ActorJWTProvider) []*apiv1alpha1.SessionProvider {
	var providers []*apiv1alpha1.SessionProvider
	for _, instance := range slices.Sorted(maps.Keys(actorJWT)) {
		configured := actorJWT[instance]
		providers = append(providers, &apiv1alpha1.SessionProvider{
			Hostname: configured.Hostname, Scheme: cmp.Or(configured.Scheme, "Bearer"), PathPrefix: configured.PathPrefix,
			//nolint:staticcheck // audience mirrors provider for clients that read it before provider existed.
			Provider: instance, Audience: instance,
		})
	}
	return providers
}

// ActorJWTHosts lists the hosts of providers on which the egress gateway
// hands the session's Actor its own JWT for every request, whatever its path:
// a runtime sends no caller credential there, since the gateway replaces it.
// The list is sorted and holds each host once.
func ActorJWTHosts(actorJWT map[string]egress.ActorJWTProvider, providers []*apiv1alpha1.SessionProvider) []string {
	var hosts []string
	for _, provider := range providers {
		if _, verifies := actorJWTVerifier(actorJWT, provider); verifies && provider.GetPathPrefix() == "" {
			hosts = append(hosts, strings.ToLower(provider.GetHostname()))
		}
	}
	slices.Sort(hosts)
	return slices.Compact(hosts)
}

// repointActor moves a suspended Actor onto the ActorTemplate of current and
// gives it that revision's egress allowlist; an Actor already on the template
// keeps it. Substrate refuses, without effect, an Actor that is no longer
// suspended and a template whose sandbox config or durable volumes differ,
// since the Actor's data could not be restored onto it: that returns a nil
// Actor and the Actor stays on its prepared revision.
func (w *ActorWorkflow) repointActor(ctx context.Context, sessionID string, current *database.RuntimeRevision, policy *ateapipb.EgressPolicy) (*ateapipb.Actor, error) {
	atespace, name := current.ActorTemplateAtespace, substrate.ActorName(sessionID)
	actor, err := w.actors.RepointActor(ctx, atespace, name, current.ActorTemplateAtespace, current.ActorTemplateName)
	if status.Code(err) == codes.FailedPrecondition {
		logging.FromContext(ctx).InfoContext(ctx, "the session stays on its prepared revision: Substrate refused its agent's current ActorTemplate",
			"session_id", sessionID, "revision", current.Revision, "reason", status.Convert(err).Message())
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("repoint Actor %s/%s to ActorTemplate %s: %w", atespace, name, current.ActorTemplateName, err)
	}
	if !validActorIdentity(actor, current, name) || actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
		return nil, fmt.Errorf("repoint Actor %s/%s returned unexpected identity or state", atespace, name)
	}
	if err := w.actors.ReplaceActorEgressPolicy(ctx, atespace, name, policy); err != nil {
		return nil, fmt.Errorf("replace Actor %s/%s egress policy: %w", atespace, name, err)
	}
	return actor, nil
}

// RepointQuiesced moves the quiesced Actor of a READY session onto its agent's
// current revision, before a turn wakes it or because nothing has for a while,
// and returns the session as it then is. The caller keeps every turn of its
// own off the session meanwhile: the gateway holds the turn's dispatch, the
// sweep visits only settled sessions. A session whose Actor is current, live,
// paused, not its own, or refused by Substrate is returned unchanged. Two
// callers may move the same Actor, the gateway before a turn and the sweep of
// superseded revisions: the second finds the Actor moved and records what the
// first has yet to. A lifecycle operation that claimed the session meanwhile
// wins: the Actor goes back to the template of the revision the session
// records.
func (w *ActorWorkflow) RepointQuiesced(ctx context.Context, session *apiv1alpha1.Session) (*apiv1alpha1.Session, error) {
	if session.GetState() != apiv1alpha1.RuntimeState_RUNTIME_STATE_READY {
		return session, nil
	}
	revision, err := w.store.GetRuntimeRevision(ctx, session.GetPreparedRevision())
	if err != nil {
		return nil, fmt.Errorf("load prepared revision: %w", err)
	}
	current, policy, err := w.repointTarget(ctx, session, revision)
	if err != nil || current == nil {
		return session, err
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(session.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if err != nil {
		return nil, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED || (!validActorIdentity(actor, revision, name) && !validActorIdentity(actor, current, name)) {
		return session, nil
	}
	if _, err := w.store.GetSessionForRuntime(ctx, session.GetId(), actor.GetMetadata().GetUid()); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return session, nil
		}
		return nil, fmt.Errorf("verify runtime actor UID: %w", err)
	}
	repointed, err := w.repointActor(ctx, session.GetId(), current, policy)
	if err != nil || repointed == nil {
		return session, err
	}
	var updated *apiv1alpha1.Session
	if len(session.GetToolImages()) > 0 {
		updated, err = w.store.RepointToolsetSession(ctx, session.GetId(), revision.Revision, *current)
	} else {
		updated, err = w.store.RepointSession(ctx, session.GetId(), revision.Revision, current.Revision)
	}
	if errors.Is(err, database.ErrConflict) && updated.GetPreparedRevision() == revision.Revision {
		previous, policyErr := w.sessionPolicy(ctx, atespace, revision, session.GetProviders(), betweenTurns)
		if policyErr == nil {
			_, policyErr = w.repointActor(ctx, session.GetId(), revision, previous)
		}
		return nil, errors.Join(err, policyErr)
	}
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// failPreparation releases only unissued work. If another caller won, observe
// the same generation instead. Completion returns current state; supersession
// returns a conflict. A local preparation error cannot clear a newer operation.
func (w *ActorWorkflow) failPreparation(ctx context.Context, admitted *database.SessionOperation, cause error) (*apiv1alpha1.Session, error) {
	if admitted.ExecutorID != uuid.Nil {
		return nil, cause // An earlier attempt may have issued work; retain its intent.
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := w.store.FinishSessionOperation(finishCtx, admitted.Instance.Id, admitted.ID, uuid.Nil, "", "", "lifecycle preparation failed")
	if errors.Is(err, database.ErrConflict) {
		operation, readErr := w.store.GetSessionOperation(finishCtx, admitted.Instance.Id, admitted.ID)
		if readErr != nil {
			return nil, errors.Join(cause, err, readErr)
		}
		return operationOutcome(operation)
	}
	return nil, errors.Join(cause, err)
}

func operationOutcome(operation *database.SessionOperation) (*apiv1alpha1.Session, error) {
	if operation.Instance.Operation == apiv1alpha1.RuntimeOperation_RUNTIME_OPERATION_NONE {
		return operation.Instance, nil
	}
	return nil, fmt.Errorf("lifecycle operation %s is pending; runtime effects may be unresolved: %w", operation.ID, database.ErrConflict)
}

// Verify the recorded actor UID before issuing lifecycle work. Names and
// templates alone also match an externally replaced actor, which we must not
// adopt or checkpoint as this session's runtime.
func (w *ActorWorkflow) verifyActor(ctx context.Context, session *apiv1alpha1.Session, revision *database.RuntimeRevision, actor *ateapipb.Actor) error {
	if !validActorIdentity(actor, revision, substrate.ActorName(session.Id)) {
		return fmt.Errorf("runtime actor identity or template changed")
	}
	if _, err := w.store.GetSessionForRuntime(ctx, session.Id, actor.GetMetadata().GetUid()); err != nil {
		return fmt.Errorf("verify runtime actor UID: %w", err)
	}
	return nil
}

func validActorIdentity(actor *ateapipb.Actor, revision *database.RuntimeRevision, name string) bool {
	metadata, ref := actor.GetMetadata(), actor.GetActorTemplate()
	return metadata.GetName() == name && metadata.GetAtespace() == revision.ActorTemplateAtespace && metadata.GetUid() != "" &&
		ref.GetAtespace() == revision.ActorTemplateAtespace && ref.GetName() == revision.ActorTemplateName
}
