package agentinstance

import (
	"context"
	"testing"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestActorWorkflowLifecycle(t *testing.T) {
	instance := &apiv1alpha1.AgentInstance{
		Id:               "8bd650a8-9775-488f-8bc1-0d52bf7bdcab",
		PreparedRevision: "revision-1", State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING,
		Operation: apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE,
	}
	store := &lifecycleTestStore{
		instance: instance,
		revision: &database.RuntimeRevision{
			Revision: "revision-1", ActorTemplateAtespace: "team-a", ActorTemplateName: "assistant-kagent-revision",
		},
	}
	actors := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{}}
	workflow := NewActorWorkflow(store, actors)

	created, err := workflow.Create(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	if created.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY || created.GetA2AAuthority() == "" {
		t.Fatalf("created instance = %+v", created)
	}
	if len(actors.actors) != 1 {
		t.Fatalf("actors = %v", actors.actors)
	}
	if actor := actors.actors[actorKey("team-a", substrate.ActorName(instance.GetId()))]; actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
		t.Fatalf("created Actor status = %s", actor.GetStatus().GetState())
	}
	actors.actors[actorKey("team-a", substrate.ActorName(instance.GetId()))].Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	if err := workflow.Pause(context.Background(), created); err != nil {
		t.Fatal(err)
	}
	if actor := actors.actors[actorKey("team-a", substrate.ActorName(instance.GetId()))]; actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_PAUSED {
		t.Fatalf("paused Actor status = %s", actor.GetStatus().GetState())
	}
	boundary, err := workflow.Quiesce(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	if created.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY || boundary.URI != "s3://snapshots/snapshot-1" {
		t.Fatalf("quiesced instance = %+v, boundary = %+v", created, boundary)
	}

	suspended, err := workflow.Suspend(context.Background(), created)
	if err != nil {
		t.Fatal(err)
	}
	if suspended.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_SUSPENDED || suspended.GetOperation() != apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED {
		t.Fatalf("suspended instance = %+v", suspended)
	}
	if actor := actors.actors[actorKey("team-a", substrate.ActorName(instance.GetId()))]; actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED {
		t.Fatalf("suspended Actor status = %s", actor.GetStatus().GetState())
	}

	resumed, err := workflow.Resume(context.Background(), suspended)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY || resumed.GetOperation() != apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED {
		t.Fatalf("resumed instance = %+v", resumed)
	}

	deleted, err := workflow.Delete(context.Background(), resumed)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_DELETED || store.instance != nil || len(actors.actors) != 0 {
		t.Fatalf("deleted instance = %+v, actors = %v", deleted, actors.actors)
	}
}

func TestActorWorkflowIdle(t *testing.T) {
	instance := &apiv1alpha1.AgentInstance{
		Id: "idle-1", PreparedRevision: "revision-1", State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING,
		Operation: apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE,
	}
	store := &lifecycleTestStore{
		instance: instance,
		revision: &database.RuntimeRevision{
			Revision: "revision-1", ActorTemplateAtespace: "team-a", ActorTemplateName: "assistant-kagent-revision",
		},
	}
	actors := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{}}
	workflow := NewActorWorkflow(store, actors)
	ready, err := workflow.Create(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	actor := actors.actors[actorKey("team-a", substrate.ActorName(instance.GetId()))]
	for state, want := range map[ateapipb.ActorState]bool{
		ateapipb.ActorState_ACTOR_STATE_PAUSED:     true,
		ateapipb.ActorState_ACTOR_STATE_SUSPENDED:  true,
		ateapipb.ActorState_ACTOR_STATE_RUNNING:    false,
		ateapipb.ActorState_ACTOR_STATE_RESUMING:   false,
		ateapipb.ActorState_ACTOR_STATE_SUSPENDING: false,
		ateapipb.ActorState_ACTOR_STATE_CRASHED:    false,
	} {
		actor.Status.State = state
		idle, err := workflow.Idle(context.Background(), ready)
		if err != nil || idle != want {
			t.Fatalf("Idle() with actor %s = %v, %v; want %v", state, idle, err, want)
		}
	}
	actor.ActorTemplate.Name = "other-template"
	if _, err := workflow.Idle(context.Background(), ready); err == nil {
		t.Fatal("Idle() accepted an actor of another template")
	}
	delete(actors.actors, actorKey("team-a", substrate.ActorName(instance.GetId())))
	if _, err := workflow.Idle(context.Background(), ready); status.Code(err) != codes.NotFound {
		t.Fatalf("Idle() without an actor = %v, want NotFound", err)
	}
}

func TestActorWorkflowForkCreatesSuspendedActorFromCheckpoint(t *testing.T) {
	instance := &apiv1alpha1.AgentInstance{
		Id: "fork-1", PreparedRevision: "revision-1",
		State:     apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING,
		Operation: apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE,
	}
	store := &lifecycleTestStore{
		instance: instance,
		revision: &database.RuntimeRevision{
			Revision: "revision-1", ActorTemplateAtespace: "team-a", ActorTemplateName: "assistant-kagent-revision",
		},
	}
	actors := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{}}
	snapshot := &database.AgentInstanceTaskSnapshot{Atespace: "team-a", URI: "s3://snapshots/snapshot-1", ContentScope: "DATA"}
	fork, err := NewActorWorkflow(store, actors).Fork(context.Background(), instance, snapshot, "checkpoint-018f47a2-4efb-7c21-a848-123456789abc")
	if err != nil {
		t.Fatal(err)
	}
	actor := actors.actors[actorKey("team-a", substrate.ActorName(instance.GetId()))]
	if fork.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY ||
		actor.GetStatus().GetState() != ateapipb.ActorState_ACTOR_STATE_SUSPENDED ||
		actor.GetSourceTag().GetName() != "checkpoint-018f47a2-4efb-7c21-a848-123456789abc" {
		t.Fatalf("fork = %+v, actor = %+v", fork, actor)
	}
	instance.State = apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING
	store.instance = instance
	actor.Status.ExternalSnapshot.SnapshotUri = "s3://snapshots/later-turn"
	if _, err := NewActorWorkflow(store, actors).Fork(context.Background(), instance, snapshot, "checkpoint-018f47a2-4efb-7c21-a848-123456789abc"); err == nil {
		t.Fatal("Fork() accepted a different external snapshot")
	}
	actor.Status.ExternalSnapshot.SnapshotUri = snapshot.URI
	actor.SourceTag.Name = "wrong-tag"
	if _, err := NewActorWorkflow(store, actors).Fork(context.Background(), instance, snapshot, "checkpoint-018f47a2-4efb-7c21-a848-123456789abc"); err == nil {
		t.Fatal("Fork() accepted an existing Actor with the wrong snapshot tag")
	}
}

type lifecycleTestStore struct {
	instance *apiv1alpha1.AgentInstance
	revision *database.RuntimeRevision
}

func (s *lifecycleTestStore) GetRuntimeRevision(context.Context, string) (*database.RuntimeRevision, error) {
	return s.revision, nil
}

func (s *lifecycleTestStore) TransitionAgentInstance(_ context.Context, instance *apiv1alpha1.AgentInstance, expectedState apiv1alpha1.AgentInstanceState, expectedOperation apiv1alpha1.AgentInstanceOperation) (*apiv1alpha1.AgentInstance, error) {
	if s.instance.GetState() != expectedState || s.instance.GetOperation() != expectedOperation {
		return s.instance, database.ErrConflict
	}
	s.instance = proto.Clone(instance).(*apiv1alpha1.AgentInstance)
	return s.instance, nil
}

func (s *lifecycleTestStore) DeleteAgentInstance(context.Context, string) error {
	s.instance = nil
	return nil
}

type lifecycleTestActors struct {
	actors map[string]*ateapipb.Actor
	// getErr fails every GetActor; suspendCalls counts the suspends asked
	// for; deletedAnyState is the any_state flag of the last DeleteActor.
	getErr          error
	suspendCalls    int
	deletedAnyState bool
}

func actorKey(atespace, name string) string { return atespace + "/" + name }

func (*lifecycleTestActors) EnsureAtespace(context.Context, string) error { return nil }

func (a *lifecycleTestActors) GetActor(_ context.Context, atespace, name string) (*ateapipb.Actor, error) {
	if a.getErr != nil {
		return nil, a.getErr
	}
	actor := a.actors[actorKey(atespace, name)]
	if actor == nil {
		return nil, status.Error(codes.NotFound, "missing")
	}
	return actor, nil
}

func (a *lifecycleTestActors) CreateActor(_ context.Context, atespace, name, templateNamespace, templateName string) (*ateapipb.Actor, error) {
	actor := &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: atespace, Name: name, Uid: "actor-uid"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: templateNamespace, Name: templateName},
		Status:        &ateapipb.ActorStatus{State: ateapipb.ActorState_ACTOR_STATE_SUSPENDED},
	}
	a.actors[actorKey(atespace, name)] = actor
	return actor, nil
}

func (a *lifecycleTestActors) CreateActorFromTag(_ context.Context, atespace, name, templateNamespace, templateName, tagAtespace, tagName string) (*ateapipb.Actor, error) {
	actor := &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: atespace, Name: name, Uid: "actor-uid"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: templateNamespace, Name: templateName},
		SourceTag:     &ateapipb.ObjectRef{Atespace: tagAtespace, Name: tagName},
		Status: &ateapipb.ActorStatus{
			State:            ateapipb.ActorState_ACTOR_STATE_SUSPENDED,
			ExternalSnapshot: &ateapipb.ExternalSnapshot{SnapshotUri: "s3://snapshots/snapshot-1", ContentScope: ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA},
		},
	}
	a.actors[actorKey(atespace, name)] = actor
	return actor, nil
}

func (a *lifecycleTestActors) ResumeActor(_ context.Context, atespace, name string) (*ateapipb.Actor, error) {
	actor := a.actors[actorKey(atespace, name)]
	actor.Status.State = ateapipb.ActorState_ACTOR_STATE_RUNNING
	return actor, nil
}

func (a *lifecycleTestActors) PauseActor(_ context.Context, atespace, name string) (*ateapipb.Actor, error) {
	actor := a.actors[actorKey(atespace, name)]
	actor.Status.State = ateapipb.ActorState_ACTOR_STATE_PAUSED
	return actor, nil
}

func (a *lifecycleTestActors) SuspendActor(_ context.Context, atespace, name string) (*ateapipb.Actor, error) {
	a.suspendCalls++
	actor := a.actors[actorKey(atespace, name)]
	actor.Status.State = ateapipb.ActorState_ACTOR_STATE_SUSPENDED
	actor.Status.ExternalSnapshot = &ateapipb.ExternalSnapshot{SnapshotUri: "s3://snapshots/snapshot-1", ContentScope: ateapipb.SnapshotContentScope_SNAPSHOT_CONTENT_SCOPE_DATA}
	return actor, nil
}

func (a *lifecycleTestActors) DeleteActor(_ context.Context, atespace, name string, anyState bool) error {
	a.deletedAnyState = anyState
	delete(a.actors, actorKey(atespace, name))
	return nil
}

func TestQuiesceRejectsWrongActorIdentity(t *testing.T) {
	instance := &apiv1alpha1.AgentInstance{Id: "instance-1", PreparedRevision: "revision-1"}
	store := &lifecycleTestStore{revision: &database.RuntimeRevision{ActorTemplateAtespace: "team-a"}}
	actors := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{
		actorKey("team-a", substrate.ActorName(instance.Id)): {
			Metadata: &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "different-actor", Uid: "actor-uid"},
			Status:   &ateapipb.ActorStatus{},
		},
	}}
	if _, err := NewActorWorkflow(store, actors).Quiesce(t.Context(), instance); err == nil {
		t.Fatal("Quiesce() accepted the wrong Actor")
	}
}

func TestFinishCreatePreservesLaterLifecycle(t *testing.T) {
	creating := &apiv1alpha1.AgentInstance{
		Id: "instance", State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING,
		Operation: apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_CREATE,
		Failure:   &apiv1alpha1.Failure{Message: "previous failure"},
	}
	for name, current := range map[string]*apiv1alpha1.AgentInstance{
		"creating":          proto.CloneOf(creating),
		"already ready":     {Id: "instance", State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY, A2AAuthority: "original"},
		"deleting":          {Id: "instance", State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_DELETING, Operation: apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_DELETE},
		"another operation": {Id: "instance", State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING, Operation: apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_DELETE},
	} {
		t.Run(name, func(t *testing.T) {
			store := &lifecycleTestStore{instance: current}
			got, err := NewActorWorkflow(store, nil).finishCreate(t.Context(), creating, "runtime.example")
			if err != nil {
				t.Fatal(err)
			}
			if name == "creating" {
				if got.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY ||
					got.GetOperation() != apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED ||
					got.GetA2AAuthority() != "runtime.example" || got.GetFailure() != nil {
					t.Fatalf("creation result = %v", got)
				}
			} else if !proto.Equal(current, got) {
				t.Fatalf("late completion changed current state: got %v, want %v", got, current)
			}
		})
	}
	if creating.GetFailure() == nil || creating.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING {
		t.Fatal("creation changed the caller's instance")
	}
}

func lostRuntimeTestFixture(actor *ateapipb.Actor) (*apiv1alpha1.AgentInstance, *lifecycleTestStore, *lifecycleTestActors) {
	instance := &apiv1alpha1.AgentInstance{
		Id: "8bd650a8-9775-488f-8bc1-0d52bf7bdcab", PreparedRevision: "revision-1",
		State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY, A2AAuthority: "runtime.example",
	}
	store := &lifecycleTestStore{
		instance: proto.CloneOf(instance),
		revision: &database.RuntimeRevision{Revision: "revision-1", ActorTemplateAtespace: "team-a", ActorTemplateName: "assistant-kagent-revision"},
	}
	actors := &lifecycleTestActors{actors: map[string]*ateapipb.Actor{}}
	if actor != nil {
		actor.Metadata = &ateapipb.ResourceMetadata{Atespace: "team-a", Name: substrate.ActorName(instance.GetId()), Uid: "actor-uid"}
		actor.ActorTemplate = &ateapipb.ObjectRef{Atespace: "team-a", Name: "assistant-kagent-revision"}
		actors.actors[actorKey("team-a", substrate.ActorName(instance.GetId()))] = actor
	}
	return instance, store, actors
}

// A crashed or missing Actor never takes another turn; a paused or running
// one may. Not being able to ask Substrate is neither.
func TestActorWorkflowRuntimeLost(t *testing.T) {
	name := substrate.ActorName("8bd650a8-9775-488f-8bc1-0d52bf7bdcab")
	for _, test := range []struct {
		name   string
		actor  *ateapipb.Actor
		getErr error
		lost   bool
		cause  string
	}{
		{name: "crashed", actor: &ateapipb.Actor{Status: &ateapipb.ActorStatus{State: ateapipb.ActorState_ACTOR_STATE_CRASHED}}, lost: true, cause: "Actor team-a/" + name + " crashed"},
		{name: "gone", lost: true, cause: "Actor team-a/" + name + " not found"},
		{name: "paused", actor: &ateapipb.Actor{Status: &ateapipb.ActorStatus{State: ateapipb.ActorState_ACTOR_STATE_PAUSED}}},
		{name: "running", actor: &ateapipb.Actor{Status: &ateapipb.ActorStatus{State: ateapipb.ActorState_ACTOR_STATE_RUNNING}}},
		{name: "unknown", actor: &ateapipb.Actor{Status: &ateapipb.ActorStatus{State: ateapipb.ActorState_ACTOR_STATE_CRASHED}}, getErr: status.Error(codes.Unavailable, "ate-api is rolling")},
	} {
		t.Run(test.name, func(t *testing.T) {
			instance, store, actors := lostRuntimeTestFixture(test.actor)
			actors.getErr = test.getErr
			cause, lost, err := NewActorWorkflow(store, actors).RuntimeLost(t.Context(), instance)
			if test.getErr != nil {
				if err == nil || lost {
					t.Fatalf("RuntimeLost() = %q, %v, %v; want the Substrate error and not lost", cause, lost, err)
				}
				return
			}
			if err != nil || lost != test.lost || cause != test.cause {
				t.Fatalf("RuntimeLost() = %q, %v, %v; want %q, %v", cause, lost, err, test.cause, test.lost)
			}
		})
	}
}

func TestActorWorkflowMarkRuntimeLostFailsTheInstanceOnce(t *testing.T) {
	instance, store, actors := lostRuntimeTestFixture(nil)
	workflow := NewActorWorkflow(store, actors)
	message := "runtime lost: Actor team-a/ai-8bd650a8 crashed: actor unavailable"

	failed, err := workflow.MarkRuntimeLost(t.Context(), instance, message)
	if err != nil {
		t.Fatal(err)
	}
	if failed.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED ||
		failed.GetOperation() != apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED ||
		failed.GetFailure().GetReason() != "RuntimeLost" || failed.GetFailure().GetMessage() != message ||
		failed.GetA2AAuthority() != "runtime.example" || !proto.Equal(failed, store.instance) {
		t.Fatalf("marked instance = %v, stored = %v", failed, store.instance)
	}
	if instance.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY || instance.GetFailure() != nil {
		t.Fatal("MarkRuntimeLost() changed the caller's instance")
	}

	// A second turn that finds the runtime lost records nothing new.
	again, err := workflow.MarkRuntimeLost(t.Context(), instance, "runtime lost: later")
	if err != nil || again.GetFailure().GetMessage() != message {
		t.Fatalf("second MarkRuntimeLost() = %v, %v; want the first record kept", again, err)
	}

	// An instance another operation has moved on is not failed underneath it.
	store.instance.State, store.instance.Failure = apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_SUSPENDED, nil
	if _, err := workflow.MarkRuntimeLost(t.Context(), instance, message); err == nil || store.instance.GetFailure() != nil {
		t.Fatalf("MarkRuntimeLost() on a suspended instance = %v, stored = %v", err, store.instance)
	}
}

// A live Actor is suspended before it is deleted; a paused or crashed one is
// deleted as it is — suspending a paused Actor needs the node its checkpoint
// is on, which is the node that may be gone.
func TestActorWorkflowDeleteSuspendsOnlyLiveActors(t *testing.T) {
	for _, test := range []struct {
		state    ateapipb.ActorState
		suspends int
		anyState bool
	}{
		{state: ateapipb.ActorState_ACTOR_STATE_RUNNING, suspends: 1},
		{state: ateapipb.ActorState_ACTOR_STATE_SUSPENDED},
		{state: ateapipb.ActorState_ACTOR_STATE_PAUSED, anyState: true},
		{state: ateapipb.ActorState_ACTOR_STATE_CRASHED},
	} {
		t.Run(test.state.String(), func(t *testing.T) {
			instance, store, actors := lostRuntimeTestFixture(&ateapipb.Actor{Status: &ateapipb.ActorStatus{State: test.state}})
			if test.state == ateapipb.ActorState_ACTOR_STATE_CRASHED {
				instance.State, store.instance.State = apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED, apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED
			}
			deleted, err := NewActorWorkflow(store, actors).Delete(t.Context(), instance)
			if err != nil {
				t.Fatal(err)
			}
			if deleted.GetState() != apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_DELETED || store.instance != nil || len(actors.actors) != 0 {
				t.Fatalf("deleted instance = %v, stored = %v, actors = %v", deleted, store.instance, actors.actors)
			}
			if actors.suspendCalls != test.suspends || actors.deletedAnyState != test.anyState {
				t.Fatalf("suspends = %d, any_state = %v; want %d, %v", actors.suspendCalls, actors.deletedAnyState, test.suspends, test.anyState)
			}
		})
	}
}
