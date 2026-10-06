package agentinstance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/uuid"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type workflowStore interface {
	GetAgentInstanceCheckpointSnapshot(context.Context, string, string) (*database.AgentInstanceTaskSnapshot, string, error)
	GetRuntimeRevision(context.Context, string) (*database.RuntimeRevision, error)
	GetCurrentRuntimeRevision(context.Context, string) (*database.RuntimeRevision, error)
	RepointAgentInstanceOperation(context.Context, string, uuid.UUID, uuid.UUID, string) error
	RepointAgentInstance(context.Context, string, string, string) (*apiv1alpha1.AgentInstance, error)
	BeginAgentInstanceOperation(context.Context, string, apiv1alpha1.AgentInstanceOperation) (*database.InstanceOperation, error)
	ClaimAgentInstanceOperation(context.Context, string, uuid.UUID, uuid.UUID) (bool, error)
	FinishAgentInstanceOperation(context.Context, string, uuid.UUID, uuid.UUID, string, string) (*apiv1alpha1.AgentInstance, error)
	FailAgentInstanceOperation(context.Context, string, uuid.UUID, uuid.UUID, *apiv1alpha1.Failure) (*apiv1alpha1.AgentInstance, error)
	GetAgentInstanceOperation(context.Context, string, uuid.UUID) (*database.InstanceOperation, error)
	// TransitionAgentInstance moves an instance between stable states outside a
	// lifecycle operation: the runtime-lost marker, which claims nothing.
	TransitionAgentInstance(context.Context, *apiv1alpha1.AgentInstance, apiv1alpha1.AgentInstanceState, apiv1alpha1.AgentInstanceOperation) (*apiv1alpha1.AgentInstance, error)
}

// ErrRuntimeLost reports a lifecycle operation that Substrate answered can
// never succeed: the instance records the loss as FAILED and stays deletable.
var ErrRuntimeLost = errors.New("AgentInstance runtime is lost")

type actorClient interface {
	EnsureActorEgressPolicy(context.Context, string, string, *ateapipb.EgressPolicy) error
	ReplaceActorEgressPolicy(context.Context, string, string, *ateapipb.EgressPolicy) error
	GetActor(context.Context, string, string) (*ateapipb.Actor, error)
	CreateActor(context.Context, string, string, string, string) (*ateapipb.Actor, error)
	CreateActorFromTag(context.Context, string, string, string, string, string, string) (*ateapipb.Actor, error)
	ResumeActor(context.Context, string, string) (*ateapipb.Actor, error)
	RepointActor(context.Context, string, string, string, string) (*ateapipb.Actor, error)
	PauseActor(context.Context, string, string) (*ateapipb.Actor, error)
	SuspendActor(context.Context, string, string) (*ateapipb.Actor, error)
	DeleteActor(context.Context, string, string, bool) error
	ListAllWorkers(context.Context) ([]*ateapipb.Worker, error)
}

// ActorWorkflow runs the imperative Substrate operations behind AgentInstance
// lifecycle RPCs. Only the claiming caller issues lifecycle mutations; others
// observe current completion or receive a pending/superseded-operation error.
type ActorWorkflow struct {
	store  workflowStore
	actors actorClient
}

func NewActorWorkflow(store workflowStore, actors actorClient) *ActorWorkflow {
	return &ActorWorkflow{store: store, actors: actors}
}

// Pause checkpoints the runtime on its current worker without changing the
// AgentInstance logical state or persisting A2A task state.
func (w *ActorWorkflow) Pause(ctx context.Context, instance *apiv1alpha1.AgentInstance) error {
	revision, err := w.store.GetRuntimeRevision(ctx, instance.GetPreparedRevision())
	if err != nil {
		return fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(instance.GetId())
	actor, err := w.actors.PauseActor(ctx, atespace, name)
	if err != nil {
		return fmt.Errorf("pause Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_PAUSED {
		return fmt.Errorf("pause Actor %s/%s returned status %s", atespace, name, actor.GetStatus().GetState())
	}
	return nil
}

// Quiesce durably suspends the runtime without changing the AgentInstance's
// logical READY state and records its external snapshot URI. Only a checkpoint
// retains a copy after the Actor advances.
func (w *ActorWorkflow) Quiesce(ctx context.Context, instance *apiv1alpha1.AgentInstance) (*database.AgentInstanceTaskSnapshot, error) {
	revision, err := w.store.GetRuntimeRevision(ctx, instance.GetPreparedRevision())
	if err != nil {
		return nil, fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(instance.GetId())
	actor, err := w.actors.SuspendActor(ctx, atespace, name)
	if err != nil {
		return nil, fmt.Errorf("suspend Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
		return nil, fmt.Errorf("suspend Actor %s/%s returned status %s", atespace, name, actor.GetStatus().GetState())
	}
	metadata := actor.GetMetadata()
	if metadata.GetAtespace() != atespace || metadata.GetName() != name || metadata.GetUid() == "" {
		return nil, fmt.Errorf("suspend actor %s/%s returned invalid identity", atespace, name)
	}
	snapshot := actor.GetStatus().GetExternalSnapshot()
	if snapshot.GetSnapshotUri() == "" {
		return nil, fmt.Errorf("suspend Actor %s/%s returned no snapshot", atespace, name)
	}
	scope := snapshot.GetContentScope()
	if scope != ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_FULL && scope != ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA {
		return nil, fmt.Errorf("actor %s/%s returned invalid snapshot content scope %s", atespace, name, scope)
	}
	return &database.AgentInstanceTaskSnapshot{
		Atespace: atespace, URI: snapshot.GetSnapshotUri(),
		ContentScope: strings.TrimPrefix(scope.String(), "SNAPSHOT_CONTENT_SCOPE_"),
	}, nil
}

// RuntimeLost reports whether the instance's runtime can no longer take a
// turn: Substrate reports its Actor CRASHED, or the Actor is gone. Substrate
// crashes an Actor it cannot bring back — a restore that ran out of time, a
// worker that vanished under it, a paused checkpoint whose node is gone — and
// a crashed Actor never resumes, so every later message would fail the way
// the last one did. The cause names the Actor and what became of it. An
// answer Substrate cannot give is returned as the error it is: not knowing is
// not the same as lost.
func (w *ActorWorkflow) RuntimeLost(ctx context.Context, instance *apiv1alpha1.AgentInstance) (string, bool, error) {
	revision, err := w.store.GetRuntimeRevision(ctx, instance.GetPreparedRevision())
	if err != nil {
		return "", false, fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(instance.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if status.Code(err) == codes.NotFound {
		return fmt.Sprintf("Actor %s/%s not found", atespace, name), true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() == ateapipb.ActorState_ACTOR_STATE_CRASHED {
		return fmt.Sprintf("Actor %s/%s crashed", atespace, name), true, nil
	}
	return "", false, nil
}

// MarkRuntimeLost records that the instance's runtime is gone: the instance
// leaves READY for FAILED with the reason and the message, so a client reads
// the loss without sending a message and the gateway refuses later sends
// without dialing a runtime that cannot answer. The transcript is untouched;
// the instance stays readable and deletable. An instance that already
// records the loss, or that another operation has moved on since, is
// returned as it is.
func (w *ActorWorkflow) MarkRuntimeLost(ctx context.Context, instance *apiv1alpha1.AgentInstance, message string) (*apiv1alpha1.AgentInstance, error) {
	next := proto.CloneOf(instance)
	next.State = apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED
	next.Operation = apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED
	next.Failure = &apiv1alpha1.Failure{Reason: apia2a.FailureReasonRuntimeLost, Message: message}
	next.UpdatedAt = timestamppb.Now()
	current, err := w.store.TransitionAgentInstance(ctx, next,
		apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED)
	if errors.Is(err, database.ErrConflict) && current.GetState() == apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED &&
		current.GetFailure().GetReason() == apia2a.FailureReasonRuntimeLost {
		return current, nil
	}
	return current, err
}

// Idle reports whether the runtime holds no live process for the instance:
// paused on its worker, as Pause leaves it, or suspended to the snapshot store.
// A runtime running or resuming a turn is not idle, and neither is one that
// crashed or is being deleted. A paused runtime counts only while a worker
// still runs on its checkpoint's node: suspending it uploads the checkpoint
// through that node, and with no worker there the upload cannot complete and
// Substrate leaves the Actor suspending — a pause on a lost node is the
// node-loss handling's to crash, not a caller's to touch.
func (w *ActorWorkflow) Idle(ctx context.Context, instance *apiv1alpha1.AgentInstance) (bool, error) {
	revision, err := w.store.GetRuntimeRevision(ctx, instance.GetPreparedRevision())
	if err != nil {
		return false, fmt.Errorf("load prepared revision: %w", err)
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(instance.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if err != nil {
		return false, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	if !validActorIdentity(actor, revision, name) {
		return false, fmt.Errorf("actor %s/%s uses unexpected ActorTemplate %s/%s", atespace, name, actor.GetActorTemplate().GetAtespace(), actor.GetActorTemplate().GetName())
	}
	switch actor.GetStatus().GetState() {
	case ateapipb.ActorState_ACTOR_STATE_SUSPENDED:
		return true, nil
	case ateapipb.ActorState_ACTOR_STATE_PAUSED:
		return w.pauseNodeHasWorker(ctx, actor)
	default:
		return false, nil
	}
}

// pauseNodeHasWorker reports whether a worker runs on a node that holds the
// paused Actor's checkpoint. An Actor that records no node is left to
// Substrate to judge.
func (w *ActorWorkflow) pauseNodeHasWorker(ctx context.Context, actor *ateapipb.Actor) (bool, error) {
	nodes := actor.GetStatus().GetLocalSnapshotInfo().GetNodeVmsWithLocalSnapshots()
	if len(nodes) == 0 {
		return true, nil
	}
	workers, err := w.actors.ListAllWorkers(ctx)
	if err != nil {
		return false, fmt.Errorf("list workers: %w", err)
	}
	for _, worker := range workers {
		if slices.Contains(nodes, worker.GetNodeName()) {
			return true, nil
		}
	}
	return false, nil
}

// Create provisions the persisted instance once, using its pinned checkpoint for
// forks. Retries return current state; an uncertain prior creation blocks execution.
func (w *ActorWorkflow) Create(ctx context.Context, instance *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error) {
	return w.run(ctx, instance.GetId(), apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE)
}

// Suspend returns after the Actor and instance are suspended. A retry observes
// the same operation rather than issuing a second mutation.
func (w *ActorWorkflow) Suspend(ctx context.Context, instance *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error) {
	return w.run(ctx, instance.GetId(), apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_SUSPEND)
}

// Resume returns after the Actor is running and the instance is ready. Missing
// Actors are errors; Resume never creates replacement compute.
func (w *ActorWorkflow) Resume(ctx context.Context, instance *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error) {
	return w.run(ctx, instance.GetId(), apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_RESUME)
}

// Delete closes admission before stopping and deleting compute. It can supersede
// unissued creation, but never deletes an instance while a prior call is uncertain.
func (w *ActorWorkflow) Delete(ctx context.Context, instance *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error) {
	return w.run(ctx, instance.GetId(), apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_DELETE)
}

// run keeps lifecycle preparation separate from the durable issue boundary.
// Multiple callers may prepare using read-only calls; exactly one can authorize
// runtime mutations. Once authorized, every error retains the operation and its
// resource pins. Pause/Quiesce and A2A execution join this boundary in the later
// shared-instance-execution change; this is lifecycle serialization only.
func (w *ActorWorkflow) run(ctx context.Context, instanceID string, requestedKind apiv1alpha1.AgentInstanceOperation) (*apiv1alpha1.AgentInstance, error) {
	operation, err := w.store.BeginAgentInstanceOperation(ctx, instanceID, requestedKind)
	if err != nil {
		return nil, err
	}
	if operation.Instance.Operation == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED || operation.ExecutorID != uuid.Nil {
		return operationOutcome(operation)
	}
	kind := operation.Instance.Operation
	instance := operation.Instance
	revision, err := w.store.GetRuntimeRevision(ctx, instance.GetPreparedRevision())
	if err != nil {
		return w.failPreparation(ctx, operation, fmt.Errorf("load prepared revision: %w", err))
	}
	var snapshot *database.AgentInstanceTaskSnapshot
	var tagName string
	if kind == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE && operation.SourceCheckpointID != nil {
		snapshot, _, err = w.store.GetAgentInstanceCheckpointSnapshot(ctx, operation.SourceCheckpointID.String(), instance.Creator)
		if err != nil {
			return w.failPreparation(ctx, operation, fmt.Errorf("load pinned checkpoint: %w", err))
		}
		if snapshot == nil || snapshot.URI == "" || snapshot.Atespace == "" || snapshot.ContentScope != "DATA" {
			return w.failPreparation(ctx, operation, fmt.Errorf("fork requires a retained DATA checkpoint"))
		}
		tagName = "checkpoint-" + operation.SourceCheckpointID.String()
	}

	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(instance.Id)
	var policy *ateapipb.EgressPolicy
	if kind == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE {
		policy, err = substrate.ActorEgressPolicy(atespace, revision.EgressDestinations, revision.Credentials)
		if err != nil {
			return w.failPreparation(ctx, operation, fmt.Errorf("build Actor %s/%s egress policy: %w", atespace, name, err))
		}
	}
	actor, err := w.actors.GetActor(ctx, atespace, name)
	missing := status.Code(err) == codes.NotFound
	if err != nil && !missing {
		return w.failPreparation(ctx, operation, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err))
	}
	if missing && kind != apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE && kind != apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_DELETE {
		return w.failPreparation(ctx, operation, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err))
	}
	if !missing {
		if kind == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE {
			return w.failPreparation(ctx, operation, fmt.Errorf("refuse to adopt existing Actor %s/%s while creation is still pending", atespace, name))
		}
		if !validActorIdentity(actor, revision, name) {
			return w.failPreparation(ctx, operation, fmt.Errorf("actor %s/%s identity or template changed", atespace, name))
		}
		if kind == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_RESUME || kind == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_SUSPEND {
			switch actor.GetStatus().GetState() {
			case ateapipb.ActorState_ACTOR_STATE_RUNNING, ateapipb.ActorState_ACTOR_STATE_SUSPENDED,
				ateapipb.ActorState_ACTOR_STATE_PAUSED, ateapipb.ActorState_ACTOR_STATE_RESUMING,
				ateapipb.ActorState_ACTOR_STATE_SUSPENDING:
			default:
				return w.failPreparation(ctx, operation, fmt.Errorf("actor %s/%s cannot perform %s from %s", atespace, name, kind, actor.GetStatus().GetState()))
			}
		}
	}
	// A suspended Actor of a superseded revision resumes on the agent's current
	// one (see repointTarget).
	var current *database.RuntimeRevision
	if kind == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_RESUME && actor.GetStatus().GetState() == ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
		current, policy, err = w.repointTarget(ctx, revision)
		if err != nil {
			return w.failPreparation(ctx, operation, err)
		}
	}

	executorID := uuid.New()
	claimed, err := w.store.ClaimAgentInstanceOperation(ctx, instanceID, operation.ID, executorID)
	if err != nil {
		return nil, err
	}
	if !claimed {
		// A superseded generation returns a conflict without runtime work.
		current, err := w.store.GetAgentInstanceOperation(ctx, instanceID, operation.ID)
		if err != nil {
			return nil, err
		}
		return operationOutcome(current)
	}
	var authority string
	switch kind {
	case apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE:
		// The pinned ActorTemplate already belongs to a provisioned Atespace.
		if snapshot == nil {
			actor, err = w.actors.CreateActor(ctx, atespace, name, revision.ActorTemplateAtespace, revision.ActorTemplateName)
		} else {
			actor, err = w.actors.CreateActorFromTag(ctx, atespace, name, revision.ActorTemplateAtespace, revision.ActorTemplateName, snapshot.Atespace, tagName)
		}
		if err == nil && (!validActorIdentity(actor, revision, name) || actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED) {
			err = fmt.Errorf("created Actor %s/%s has unexpected identity or state", atespace, name)
		}
		if err == nil && snapshot != nil {
			source := actor.GetStatus().GetExternalSnapshot()
			if !proto.Equal(actor.GetSourceTag(), &ateapipb.ObjectRef{Atespace: snapshot.Atespace, Name: tagName}) ||
				source.GetSnapshotUri() != snapshot.URI || strings.TrimPrefix(source.GetContentScope().String(), "SNAPSHOT_CONTENT_SCOPE_") != snapshot.ContentScope {
				err = fmt.Errorf("fork Actor %s/%s does not match the retained checkpoint", atespace, name)
			}
		}
		if err == nil {
			err = w.actors.EnsureActorEgressPolicy(ctx, atespace, name, policy)
			if err != nil {
				err = fmt.Errorf("ensure Actor %s/%s egress policy: %w", atespace, name, err)
			}
		}
		authority = substrate.ActorHost(atespace, name, "")
	case apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_RESUME:
		if current != nil {
			var repointed *ateapipb.Actor
			repointed, err = w.repointActor(ctx, instanceID, current, policy)
			if err == nil && repointed != nil {
				err = w.store.RepointAgentInstanceOperation(ctx, instanceID, operation.ID, executorID, current.Revision)
				actor, revision = repointed, current
			}
		}
		if err == nil && actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_RUNNING {
			actor, err = w.actors.ResumeActor(ctx, atespace, name)
		}
		if err == nil && (!validActorIdentity(actor, revision, name) || actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_RUNNING) {
			err = fmt.Errorf("resume Actor %s/%s returned unexpected identity or state", atespace, name)
		}
	case apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_SUSPEND:
		if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
			actor, err = w.actors.SuspendActor(ctx, atespace, name)
		}
		if err == nil && (!validActorIdentity(actor, revision, name) || actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED) {
			err = fmt.Errorf("suspend Actor %s/%s returned unexpected identity or state", atespace, name)
		}
	case apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_DELETE:
		if !missing {
			// Substrate deletes a SUSPENDED or CRASHED Actor as it is and an
			// Actor in any other state only when asked to. A live Actor is
			// suspended first. A PAUSED Actor is deleted as it is: its
			// checkpoint is a node-local copy that suspending would first
			// upload from the node it was taken on, and when that node is gone
			// the upload never completes — the instance could not be deleted
			// at all. A CRASHED Actor has already been released.
			anyState := false
			switch actor.GetStatus().GetState() {
			case ateapipb.ActorState_ACTOR_STATE_SUSPENDED, ateapipb.ActorState_ACTOR_STATE_CRASHED, ateapipb.ActorState_ACTOR_STATE_DELETING:
			case ateapipb.ActorState_ACTOR_STATE_PAUSED:
				anyState = true
			default:
				// A live Actor is suspended first so its state is kept. One that
				// cannot be suspended — a suspend left half-way on a lost node —
				// is deleted as it is rather than not at all.
				suspended, suspendErr := w.actors.SuspendActor(ctx, atespace, name)
				switch {
				case suspendErr != nil && status.Code(suspendErr) != codes.NotFound:
					anyState = true
				case suspendErr == nil && (!validActorIdentity(suspended, revision, name) || suspended.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED):
					err = fmt.Errorf("suspend Actor %s/%s before deletion returned unexpected identity or state", atespace, name)
				}
			}
			if err == nil {
				err = w.actors.DeleteActor(ctx, atespace, name, anyState)
				if status.Code(err) == codes.NotFound {
					err = nil
				}
			}
		}
	}
	// A disconnected client must not discard an already known runtime outcome.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if kind == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_RESUME && status.Code(err) == codes.DataLoss {
		// Substrate answers DataLoss when the Actor's snapshot cannot be
		// restored, its own or the golden one it builds on: no retry can
		// resume it, so the outcome is known and the operation must not stay
		// pending, which would refuse every later resume and delete.
		message := fmt.Sprintf("%sresume Actor %s/%s: %v", apia2a.RuntimeLostMessagePrefix, atespace, name, status.Convert(err).Message())
		failed, failErr := w.store.FailAgentInstanceOperation(finishCtx, instanceID, operation.ID, executorID,
			&apiv1alpha1.Failure{Reason: apia2a.FailureReasonRuntimeLost, Message: message})
		if failErr != nil {
			return nil, errors.Join(fmt.Errorf("resume Actor %s/%s: %w", atespace, name, err), failErr)
		}
		return failed, fmt.Errorf("%s: %w", message, ErrRuntimeLost)
	}
	if err != nil {
		return nil, fmt.Errorf("perform %s on Actor %s/%s; lifecycle operation %s remains pending: %w", kind, atespace, name, operation.ID, err)
	}
	return w.store.FinishAgentInstanceOperation(finishCtx, instanceID, operation.ID, executorID, authority, "")
}

// repointTarget returns the revision an Actor of revision moves to when it
// resumes, with that revision's egress allowlist, or nil when it stays: a
// revision rendered by an older release lacks configuration the platform needs
// now, and once no instance references it the runtime revision GC collects it.
// The target is the agent's current revision in the same atespace; an Actor
// never moves between atespaces.
func (w *ActorWorkflow) repointTarget(ctx context.Context, revision *database.RuntimeRevision) (*database.RuntimeRevision, *ateapipb.EgressPolicy, error) {
	current, err := w.store.GetCurrentRuntimeRevision(ctx, revision.Revision)
	switch {
	case errors.Is(err, database.ErrNotFound):
		return nil, nil, nil
	case err != nil:
		return nil, nil, fmt.Errorf("load current revision: %w", err)
	case current.Revision == revision.Revision || current.ActorTemplateAtespace != revision.ActorTemplateAtespace:
		return nil, nil, nil
	}
	policy, err := substrate.ActorEgressPolicy(current.ActorTemplateAtespace, current.EgressDestinations, current.Credentials)
	if err != nil {
		return nil, nil, fmt.Errorf("build egress policy of revision %s: %w", current.Revision, err)
	}
	return current, policy, nil
}

// repointActor moves a suspended Actor onto the ActorTemplate of current and
// gives it that revision's egress allowlist. Substrate refuses, without effect,
// an Actor that is no longer suspended and a template whose sandbox config or
// volume layout differs, since the Actor's data could not be restored onto it:
// that returns a nil Actor and the Actor stays on its prepared revision.
func (w *ActorWorkflow) repointActor(ctx context.Context, instanceID string, current *database.RuntimeRevision, policy *ateapipb.EgressPolicy) (*ateapipb.Actor, error) {
	atespace, name := current.ActorTemplateAtespace, substrate.ActorName(instanceID)
	actor, err := w.actors.RepointActor(ctx, atespace, name, current.ActorTemplateAtespace, current.ActorTemplateName)
	if status.Code(err) == codes.FailedPrecondition {
		logging.FromContext(ctx).InfoContext(ctx, "the AgentInstance stays on its prepared revision: Substrate refused its agent's current ActorTemplate",
			"instance", instanceID, "revision", current.Revision, "reason", status.Convert(err).Message())
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

// RepointQuiesced moves the quiesced Actor of a READY instance onto its agent's
// current revision before a turn wakes it, and returns the instance as it then
// is. The caller holds the instance's runtime exclusively, so no turn of its
// own wakes the Actor meanwhile. An instance whose Actor is current, live, or
// refused by Substrate is returned unchanged. A lifecycle operation that
// claimed the instance meanwhile wins: the Actor goes back to the prepared
// revision's template.
func (w *ActorWorkflow) RepointQuiesced(ctx context.Context, instance *apiv1alpha1.AgentInstance) (*apiv1alpha1.AgentInstance, error) {
	if instance.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY {
		return instance, nil
	}
	revision, err := w.store.GetRuntimeRevision(ctx, instance.GetPreparedRevision())
	if err != nil {
		return nil, fmt.Errorf("load prepared revision: %w", err)
	}
	current, policy, err := w.repointTarget(ctx, revision)
	if err != nil || current == nil {
		return instance, err
	}
	atespace, name := revision.ActorTemplateAtespace, substrate.ActorName(instance.GetId())
	actor, err := w.actors.GetActor(ctx, atespace, name)
	if err != nil {
		return nil, fmt.Errorf("get Actor %s/%s: %w", atespace, name, err)
	}
	if actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED || !validActorIdentity(actor, revision, name) {
		return instance, nil
	}
	repointed, err := w.repointActor(ctx, instance.GetId(), current, policy)
	if err != nil || repointed == nil {
		return instance, err
	}
	updated, err := w.store.RepointAgentInstance(ctx, instance.GetId(), revision.Revision, current.Revision)
	if errors.Is(err, database.ErrConflict) {
		previous, policyErr := substrate.ActorEgressPolicy(atespace, revision.EgressDestinations, revision.Credentials)
		if policyErr == nil {
			_, policyErr = w.repointActor(ctx, instance.GetId(), revision, previous)
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
func (w *ActorWorkflow) failPreparation(ctx context.Context, admitted *database.InstanceOperation, cause error) (*apiv1alpha1.AgentInstance, error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := w.store.FinishAgentInstanceOperation(finishCtx, admitted.Instance.Id, admitted.ID, uuid.Nil, "", "lifecycle preparation failed")
	if errors.Is(err, database.ErrConflict) {
		operation, readErr := w.store.GetAgentInstanceOperation(finishCtx, admitted.Instance.Id, admitted.ID)
		if readErr != nil {
			return nil, errors.Join(cause, err, readErr)
		}
		return operationOutcome(operation)
	}
	return nil, errors.Join(cause, err)
}

func operationOutcome(operation *database.InstanceOperation) (*apiv1alpha1.AgentInstance, error) {
	if operation.Instance.Operation == apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED {
		return operation.Instance, nil
	}
	return nil, fmt.Errorf("lifecycle operation %s is pending; runtime effects may be unresolved: %w", operation.ID, database.ErrConflict)
}

func validActorIdentity(actor *ateapipb.Actor, revision *database.RuntimeRevision, name string) bool {
	metadata, ref := actor.GetMetadata(), actor.GetActorTemplate()
	return metadata.GetName() == name && metadata.GetAtespace() == revision.ActorTemplateAtespace && metadata.GetUid() != "" &&
		ref.GetAtespace() == revision.ActorTemplateAtespace && ref.GetName() == revision.ActorTemplateName
}
