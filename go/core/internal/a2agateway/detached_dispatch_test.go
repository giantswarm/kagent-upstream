package a2agateway

import (
	"context"
	"errors"
	"iter"
	"sync"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/core/internal/database"
)

// pausingGatewayRuntime reports a task working, then holds the turn until the
// test releases it and completes it with an answer.
type pausingGatewayRuntime struct {
	gatewayTestRuntime
	working chan struct{}
	release chan struct{}
	once    sync.Once
}

func newPausingGatewayRuntime() *pausingGatewayRuntime {
	return &pausingGatewayRuntime{working: make(chan struct{}), release: make(chan struct{})}
}

func (r *pausingGatewayRuntime) SendStreamingMessage(_ context.Context, _ a2aclient.ServiceParams, req *a2atype.SendMessageRequest) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		r.recordSend(req)
		task := &a2atype.Task{ID: req.Message.TaskID, ContextID: req.Message.ContextID}
		if !yield(a2atype.NewStatusUpdateEvent(task, a2atype.TaskStateWorking, nil), nil) {
			return
		}
		r.once.Do(func() { close(r.working) })
		<-r.release
		if !yield(a2atype.NewArtifactEvent(task, a2atype.NewTextPart("42")), nil) {
			return
		}
		yield(a2atype.NewStatusUpdateEvent(task, a2atype.TaskStateCompleted, nil), nil)
	}
}

func gatewayOf(t *testing.T, handler a2asrv.RequestHandler) *Gateway {
	t.Helper()
	gateway, ok := handler.(*a2asrv.InterceptedHandler).Handler.(*Gateway)
	if !ok {
		t.Fatalf("handler = %T, want the gateway", handler)
	}
	return gateway
}

func awaitTaskRun(t *testing.T, gateway *Gateway, taskID a2atype.TaskID) {
	t.Helper()
	run, ok := gateway.taskRun(gatewayTestID, taskID)
	if !ok {
		return
	}
	select {
	case <-run.done:
	case <-time.After(5 * time.Second):
		t.Fatal("task run did not finish")
	}
}

// The turn belongs to the task, not to the call that started it: a unary caller
// whose deadline expires gets its context's error, and the turn still lands.
func TestGatewaySendMessageOutlivesTheCallersDeadline(t *testing.T) {
	runtime := newPausingGatewayRuntime()
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	workflow := &gatewayTestWorkflow{}
	handler := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, workflow, gatewayTestURL)
	gateway := gatewayOf(t, handler)

	ctx, cancel := context.WithCancel(gatewayTestContext())
	go func() {
		<-runtime.working
		cancel()
	}()
	result, err := handler.SendMessage(ctx, gatewayTestRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMessage() error = %v, want the caller's cancellation", err)
	}
	task, ok := result.(*a2atype.Task)
	if !ok || task.Status.State != a2atype.TaskStateWorking || store.task.Status.State != a2atype.TaskStateWorking {
		t.Fatalf("result = %#v, stored state = %s; want the task as recorded so far", result, store.task.Status.State)
	}
	if _, owned := gateway.taskRun(gatewayTestID, task.ID); !owned || runtime.destroyed {
		t.Fatalf("caller left: run owned=%v runtime destroyed=%v; want the turn still running", owned, runtime.destroyed)
	}

	// Another caller may refuse a new message meanwhile, but not fail the turn.
	if _, err := handler.SendMessage(gatewayTestContext(), gatewayTestRequest()); !errors.Is(err, a2atype.ErrUnsupportedOperation) {
		t.Fatalf("SendMessage() during the turn = %v, want the conflict", err)
	}
	if runtime.sendCalls != 1 || store.interrupted {
		t.Fatalf("turn owned by a run: sends=%d interrupted=%v", runtime.sendCalls, store.interrupted)
	}

	close(runtime.release)
	awaitTaskRun(t, gateway, task.ID)
	stored, err := handler.GetTask(gatewayTestContext(), &a2atype.GetTaskRequest{ID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status.State != a2atype.TaskStateCompleted || len(stored.Artifacts) != 1 || stored.Artifacts[0].Parts[0].Text() != "42" {
		t.Fatalf("stored task after the caller left = %#v, want it completed with the answer", stored)
	}
	if workflow.quiesceCalls != 1 || store.active != nil || !runtime.destroyed || len(store.stored) != 4 {
		t.Fatalf("turn landed: quiesce=%d active=%#v destroyed=%v events=%d", workflow.quiesceCalls, store.active, runtime.destroyed, len(store.stored))
	}
	// The instance takes the next message.
	if _, err := handler.SendMessage(gatewayTestContext(), gatewayTestRequest()); err != nil {
		t.Fatalf("SendMessage() after the turn landed = %v", err)
	}
}

func TestGatewaySendMessageReturnsTheTurnsOutcome(t *testing.T) {
	runtime := newPausingGatewayRuntime()
	close(runtime.release)
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	workflow := &gatewayTestWorkflow{}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, workflow, gatewayTestURL)

	result, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	task, ok := result.(*a2atype.Task)
	if !ok || task.Status.State != a2atype.TaskStateCompleted || len(task.Artifacts) != 1 || len(task.History) != 1 {
		t.Fatalf("result = %#v, want the completed task with its artifact and history", result)
	}
	if store.task.Status.State != a2atype.TaskStateCompleted || workflow.quiesceCalls != 1 || !runtime.destroyed {
		t.Fatalf("turn recorded: state=%s quiesce=%d destroyed=%v", store.task.Status.State, workflow.quiesceCalls, runtime.destroyed)
	}
}

// A caller that asks to return immediately gets the task as soon as the runtime
// reports it, as a non-streaming send does; the turn goes on.
func TestGatewaySendMessageReturnsImmediatelyWhenAsked(t *testing.T) {
	runtime := newPausingGatewayRuntime()
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	handler := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, &gatewayTestWorkflow{}, gatewayTestURL)
	gateway := gatewayOf(t, handler)
	request := gatewayTestRequest()
	request.Config = &a2atype.SendMessageConfig{ReturnImmediately: true}

	result, err := handler.SendMessage(gatewayTestContext(), request)
	if err != nil {
		t.Fatal(err)
	}
	if task, ok := result.(*a2atype.Task); !ok || task.Status.State != a2atype.TaskStateWorking {
		t.Fatalf("result = %#v, want the working task", result)
	}
	close(runtime.release)
	awaitTaskRun(t, gateway, request.Message.TaskID)
	if store.task.Status.State != a2atype.TaskStateCompleted {
		t.Fatalf("stored state = %s, want the turn to land after the caller returned", store.task.Status.State)
	}
}

// A runtime that answers a send with a message still gets that message returned.
func TestGatewaySendMessageReturnsARuntimeMessage(t *testing.T) {
	answer := a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("42"))
	runtime := &messageGatewayRuntime{answer: answer}
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, &gatewayTestWorkflow{}, gatewayTestURL)

	result, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	if message, ok := result.(*a2atype.Message); !ok || message.ID != answer.ID {
		t.Fatalf("result = %#v, want the runtime's message", result)
	}
	if store.task.Status.State != a2atype.TaskStateCompleted || len(store.task.History) != 2 {
		t.Fatalf("stored task = %#v, want it completed with the answer in its history", store.task)
	}
}

type messageGatewayRuntime struct {
	gatewayTestRuntime
	answer *a2atype.Message
}

func (r *messageGatewayRuntime) SendStreamingMessage(_ context.Context, _ a2aclient.ServiceParams, req *a2atype.SendMessageRequest) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		r.recordSend(req)
		r.answer.TaskID, r.answer.ContextID = req.Message.TaskID, req.Message.ContextID
		yield(r.answer, nil)
	}
}

// A submitted task the runtime is still running, but that no run of this
// gateway records, has lost its dispatch: once the grace period is over the
// reconciler stops the runtime's turn and fails the task, so the instance takes
// the next message.
func TestGatewayFailsSubmittedTaskWhoseDispatchIsGone(t *testing.T) {
	orphan := &a2atype.Task{ID: "orphan", ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: a2atype.TaskStateSubmitted},
		Metadata: map[string]any{apia2a.TaskCreatedAtMetadataKey: time.Now().Add(-2 * dispatchGracePeriod).UTC().Format(time.RFC3339Nano)}}
	runtime := &gatewayTestRuntime{subscribeEvent: a2atype.NewStatusUpdateEvent(orphan, a2atype.TaskStateWorking, nil), task: orphan}
	store := &gatewayTestStore{instance: gatewayTestInstance(), active: orphan, interruptResult: true}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, &gatewayTestWorkflow{}, gatewayTestURL)

	if _, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest()); err != nil {
		t.Fatal(err)
	}
	if !store.interrupted || runtime.cancelCalls != 1 || !runtime.sent || runtime.sentTaskID == orphan.ID {
		t.Fatalf("orphaned dispatch: interrupted=%v runtime cancels=%d sent=%v sent task=%s", store.interrupted, runtime.cancelCalls, runtime.sent, runtime.sentTaskID)
	}
}

// Within the grace period the same picture is a dispatch that is still
// delivering its first event: the task stays.
func TestGatewayKeepsRecentSubmittedTaskTheRuntimeRuns(t *testing.T) {
	recent := &a2atype.Task{ID: "recent", ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: a2atype.TaskStateSubmitted},
		Metadata: map[string]any{apia2a.TaskCreatedAtMetadataKey: time.Now().UTC().Format(time.RFC3339Nano)}}
	runtime := &gatewayTestRuntime{subscribeEvent: a2atype.NewStatusUpdateEvent(recent, a2atype.TaskStateWorking, nil), task: recent}
	store := &gatewayTestStore{instance: gatewayTestInstance(), active: recent, interruptResult: true}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, &gatewayTestWorkflow{}, gatewayTestURL)

	if _, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest()); !errors.Is(err, a2atype.ErrUnsupportedOperation) {
		t.Fatalf("SendMessage() = %v, want the conflict", err)
	}
	if store.interrupted || runtime.cancelCalls != 0 || runtime.sent {
		t.Fatalf("recent dispatch: interrupted=%v runtime cancels=%d sent=%v", store.interrupted, runtime.cancelCalls, runtime.sent)
	}
}

// A task a run of this gateway owns is being recorded: the reconciler leaves
// it to that run without asking the runtime.
func TestGatewayKeepsSubmittedTaskALiveRunOwns(t *testing.T) {
	runtime := newPausingGatewayRuntime()
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	dialer := &gatewayTestDialer{client: gatewayTestClient(t, runtime)}
	handler := New(store, &gatewayTestAuthorizer{}, dialer, &gatewayTestWorkflow{}, gatewayTestURL)
	gateway := gatewayOf(t, handler)
	request := gatewayTestRequest()
	request.Config = &a2atype.SendMessageConfig{ReturnImmediately: true}
	if _, err := handler.SendMessage(gatewayTestContext(), request); err != nil {
		t.Fatal(err)
	}
	// Age the active task past the grace period while its run is live.
	store.active.Metadata[apia2a.TaskCreatedAtMetadataKey] = time.Now().Add(-2 * dispatchGracePeriod).UTC().Format(time.RFC3339Nano)
	store.active.Status.Timestamp = nil
	dialer.instance = nil

	if _, err := handler.SendMessage(gatewayTestContext(), gatewayTestRequest()); !errors.Is(err, database.ErrConflict) && !errors.Is(err, a2atype.ErrUnsupportedOperation) {
		t.Fatalf("SendMessage() = %v, want the conflict", err)
	}
	if dialer.instance != nil || store.interrupted || runtime.sendCalls != 1 {
		t.Fatalf("owned task: dialed=%v interrupted=%v sends=%d", dialer.instance != nil, store.interrupted, runtime.sendCalls)
	}
	close(runtime.release)
	awaitTaskRun(t, gateway, request.Message.TaskID)
}
