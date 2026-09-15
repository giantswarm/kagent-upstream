package a2agateway

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
)

func TestGatewayFailsTaskWhenRuntimeDialFails(t *testing.T) {
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	runtime := &gatewayTestRuntime{}
	dialer := &gatewayTestDialer{err: errors.New("internal.host:1234 unreachable")}
	workflow := &gatewayTestWorkflow{}
	gateway := New(store, &gatewayTestAuthorizer{}, dialer, workflow, gatewayTestURL)

	events, err := collectStream(gateway.SendStreamingMessage(gatewayTestContext(), gatewayTestRequest()))
	if err == nil || !strings.Contains(err.Error(), runtimeUnavailableMessage) || strings.Contains(err.Error(), "internal.host") {
		t.Fatalf("SendStreamingMessage() error = %v, want a hidden %q", err, runtimeUnavailableMessage)
	}
	assertDispatchFailed(t, store, workflow, events, "internal.host:1234 unreachable")

	// The instance takes the next message as soon as the runtime is reachable.
	dialer.err, dialer.client = nil, gatewayTestClient(t, runtime)
	if _, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest()); err != nil || !runtime.sent {
		t.Fatalf("SendMessage() after a failed dispatch: sent=%v err=%v", runtime.sent, err)
	}
}

func TestGatewayFailsTaskWhenRuntimeStreamFailsBeforeStart(t *testing.T) {
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	runtime := &gatewayTestRuntime{streamErr: errors.New("actor request timed out"), taskErr: a2atype.ErrTaskNotFound}
	workflow := &gatewayTestWorkflow{}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, workflow, gatewayTestURL)

	events, err := collectStream(gateway.SendStreamingMessage(gatewayTestContext(), gatewayTestRequest()))
	if err == nil || !strings.Contains(err.Error(), "actor request timed out") {
		t.Fatalf("SendStreamingMessage() error = %v, want the runtime's", err)
	}
	if len(events) != 2 {
		t.Fatalf("stream events = %#v, want the submitted task and the failed status", events)
	}
	assertDispatchFailed(t, store, workflow, events, "actor request timed out")

	runtime.streamErr = nil
	if _, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest()); err != nil || !runtime.sent {
		t.Fatalf("SendMessage() after a failed dispatch: sent=%v err=%v", runtime.sent, err)
	}
}

// A stream that fails before its first event may have lost only the response;
// a runtime that answers for the task keeps it, and recovery finds it there.
func TestGatewayKeepsTaskTheRuntimeTookWhenItsStreamFails(t *testing.T) {
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	taken := &a2atype.Task{ID: "taken", ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: a2atype.TaskStateWorking}}
	runtime := &gatewayTestRuntime{streamErr: errors.New("response lost"), task: taken}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, &gatewayTestWorkflow{}, gatewayTestURL)

	events, err := collectStream(gateway.SendStreamingMessage(gatewayTestContext(), gatewayTestRequest()))
	if err == nil || !strings.Contains(err.Error(), "response lost") || len(events) != 1 {
		t.Fatalf("SendStreamingMessage() = %#v, %v, want the submitted task and the runtime's error", events, err)
	}
	if store.task.Status.State != a2atype.TaskStateSubmitted || store.active == nil || runtime.getTaskCalls != 1 {
		t.Fatalf("task the runtime took: state=%s active=%#v GetTask calls=%d", store.task.Status.State, store.active, runtime.getTaskCalls)
	}
}

// A subscriber that finds no run for a submitted task may be racing the
// dispatch that is about to deliver it; only the dispatch itself may fail it.
func TestGatewaySubscriberKeepsSubmittedTaskTheRuntimeDoesNotKnow(t *testing.T) {
	submitted := &a2atype.Task{ID: "submitted", ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: a2atype.TaskStateSubmitted}}
	store := &gatewayTestStore{instance: gatewayTestInstance(), task: submitted, active: submitted}
	runtime := &gatewayTestRuntime{subscribeErr: a2atype.ErrTaskNotFound, taskErr: a2atype.ErrTaskNotFound}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, &gatewayTestWorkflow{}, gatewayTestURL)

	if _, err := collectStream(gateway.SubscribeToTask(gatewayTestContext(), &a2atype.SubscribeToTaskRequest{ID: submitted.ID})); err == nil {
		t.Fatal("SubscribeToTask() hid the runtime's error")
	}
	if store.task.Status.State != a2atype.TaskStateSubmitted || store.active == nil {
		t.Fatalf("subscriber failed a task it did not dispatch: state=%s active=%#v", store.task.Status.State, store.active)
	}
}

func TestGatewayFailsSubmittedTaskLeftBehindByADispatch(t *testing.T) {
	active := &a2atype.Task{ID: "active", ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: a2atype.TaskStateSubmitted},
		Metadata: map[string]any{apia2a.TaskCreatedAtMetadataKey: time.Now().Add(-2 * dispatchGracePeriod).UTC().Format(time.RFC3339Nano)}}
	runtime := &gatewayTestRuntime{taskErr: a2atype.ErrTaskNotFound, subscribeErr: a2atype.ErrTaskNotFound}
	store := &gatewayTestStore{instance: gatewayTestInstance(), active: active, interruptResult: true}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, &gatewayTestWorkflow{}, gatewayTestURL)

	if _, err := gateway.SendMessage(gatewayTestContext(), gatewayTestRequest()); err != nil {
		t.Fatal(err)
	}
	if !store.interrupted || !runtime.sent || runtime.getTaskCalls != 1 {
		t.Fatalf("orphaned submitted task: interrupted=%v sent=%v GetTask calls=%d", store.interrupted, runtime.sent, runtime.getTaskCalls)
	}
}

func collectStream(events iter.Seq2[a2atype.Event, error]) ([]a2atype.Event, error) {
	var collected []a2atype.Event
	for event, err := range events {
		if err != nil {
			return collected, err
		}
		collected = append(collected, event)
	}
	return collected, nil
}

// assertDispatchFailed checks that a failed dispatch left a failed task carrying
// the cause, released the instance's active task, and took no snapshot.
func assertDispatchFailed(t *testing.T, store *gatewayTestStore, workflow *gatewayTestWorkflow, events []a2atype.Event, cause string) {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("no stream events")
	}
	update, ok := events[len(events)-1].(*a2atype.TaskStatusUpdateEvent)
	if !ok || update.Status.State != a2atype.TaskStateFailed || update.TaskID != store.task.ID {
		t.Fatalf("last event = %#v, want the failed status of task %s", events[len(events)-1], store.task.ID)
	}
	if update.Status.Message == nil || len(update.Status.Message.Parts) == 0 || !strings.Contains(update.Status.Message.Parts[0].Text(), cause) {
		t.Fatalf("failed status message = %#v, want the cause %q", update.Status.Message, cause)
	}
	if store.task.Status.State != a2atype.TaskStateFailed || store.task.Status.Message != update.Status.Message {
		t.Fatalf("stored task = %#v, want it failed with the same message", store.task)
	}
	if store.active != nil || store.snapshot != nil || workflow.quiesceCalls != 0 {
		t.Fatalf("failed dispatch left active=%#v snapshot=%#v quiesce calls=%d", store.active, store.snapshot, workflow.quiesceCalls)
	}
}

// A stream that fails on a runtime Substrate reports gone ends the turn at
// once: the task fails with the cause under the prefix a client recognises,
// the instance records the loss, and the runtime is not asked whether it
// took the task — that question would wait out another refusal.
func TestGatewayFailsTaskAndMarksInstanceWhenRuntimeIsLost(t *testing.T) {
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	runtime := &gatewayTestRuntime{streamErr: errors.New("actor team-a/ai-8bd650a8 unavailable: actor team-a/ai-8bd650a8 crashed"), taskErr: a2atype.ErrTaskNotFound}
	workflow := &gatewayTestWorkflow{lost: true, lostCause: "Actor team-a/ai-8bd650a8 crashed"}
	workflow.onMarkLost = func(message string) {
		store.instance.State = apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED
		store.instance.Failure = &apiv1alpha1.Failure{Reason: apia2a.FailureReasonRuntimeLost, Message: message}
	}
	dialer := &gatewayTestDialer{client: gatewayTestClient(t, runtime)}
	gateway := New(store, &gatewayTestAuthorizer{}, dialer, workflow, gatewayTestURL)

	events, err := collectStream(gateway.SendStreamingMessage(gatewayTestContext(), gatewayTestRequest()))
	want := apia2a.RuntimeLostMessagePrefix + "Actor team-a/ai-8bd650a8 crashed: actor team-a/ai-8bd650a8 unavailable: actor team-a/ai-8bd650a8 crashed"
	if !errors.Is(err, a2atype.ErrInternalError) || err.Error() != want {
		t.Fatalf("SendStreamingMessage() error = %v, want %q", err, want)
	}
	if len(events) != 2 {
		t.Fatalf("stream events = %#v, want the submitted task and the failed status", events)
	}
	assertDispatchFailed(t, store, workflow, events, want)
	if runtime.getTaskCalls != 0 {
		t.Fatalf("GetTask calls = %d, want the lost runtime left alone", runtime.getTaskCalls)
	}
	if len(workflow.marked) != 1 || workflow.marked[0] != want {
		t.Fatalf("instance failures recorded = %q, want %q", workflow.marked, want)
	}

	// The instance now refuses the next message without dialing its runtime,
	// and says why in the words the loss was recorded with.
	dialer.instance, runtime.sendCalls = nil, 0
	_, err = gateway.SendMessage(gatewayTestContext(), gatewayTestRequest())
	if !errors.Is(err, a2atype.ErrUnsupportedOperation) || err.Error() != want || dialer.instance != nil || runtime.sendCalls != 0 {
		t.Fatalf("SendMessage() on the failed instance = %v, dialed=%v sent=%d; want %q without a dial", err, dialer.instance != nil, runtime.sendCalls, want)
	}
	// Its transcript stays readable.
	if task, err := gateway.GetTask(gatewayTestContext(), &a2atype.GetTaskRequest{ID: store.task.ID}); err != nil || task.Status.State != a2atype.TaskStateFailed {
		t.Fatalf("GetTask() on the failed instance = %#v, %v", task, err)
	}
}

// A runtime whose state cannot be read is not presumed lost: the stream's
// failure is recorded as before, and the runtime is asked whether it took the task.
func TestGatewayKeepsDispatchFailureWhenRuntimeStateIsUnknown(t *testing.T) {
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	runtime := &gatewayTestRuntime{streamErr: errors.New("actor request timed out"), taskErr: a2atype.ErrTaskNotFound}
	workflow := &gatewayTestWorkflow{lostErr: errors.New("ate-api is rolling")}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, workflow, gatewayTestURL)

	events, err := collectStream(gateway.SendStreamingMessage(gatewayTestContext(), gatewayTestRequest()))
	if err == nil || err.Error() != "actor request timed out" || strings.Contains(err.Error(), apia2a.RuntimeLostMessagePrefix) {
		t.Fatalf("SendStreamingMessage() error = %v, want the runtime's, not a loss", err)
	}
	assertDispatchFailed(t, store, workflow, events, "actor request timed out")
	if runtime.getTaskCalls != 1 || len(workflow.marked) != 0 || workflow.lostCalls != 1 {
		t.Fatalf("GetTask calls = %d, failures recorded = %q, RuntimeLost calls = %d", runtime.getTaskCalls, workflow.marked, workflow.lostCalls)
	}
}

// lostMidTurnRuntime reports a working task and then loses its stream, the
// way a turn ends when the worker under it goes away.
type lostMidTurnRuntime struct {
	gatewayTestRuntime
}

func (r *lostMidTurnRuntime) SendStreamingMessage(_ context.Context, _ a2aclient.ServiceParams, req *a2atype.SendMessageRequest) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		r.recordSend(req)
		task := &a2atype.Task{ID: req.Message.TaskID, ContextID: req.Message.ContextID}
		if !yield(a2atype.NewStatusUpdateEvent(task, a2atype.TaskStateWorking, nil), nil) {
			return
		}
		yield(nil, errors.New("actor team-a/ai-8bd650a8 unavailable: actor team-a/ai-8bd650a8 crashed"))
	}
}

// A turn under way when its runtime is lost fails too, instead of staying
// active until the next message finds it orphaned.
func TestGatewayFailsRunningTaskWhenRuntimeIsLost(t *testing.T) {
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	runtime := &lostMidTurnRuntime{}
	workflow := &gatewayTestWorkflow{lost: true, lostCause: "Actor team-a/ai-8bd650a8 crashed"}
	gateway := New(store, &gatewayTestAuthorizer{}, &gatewayTestDialer{client: gatewayTestClient(t, runtime)}, workflow, gatewayTestURL)

	events, err := collectStream(gateway.SendStreamingMessage(gatewayTestContext(), gatewayTestRequest()))
	if !errors.Is(err, a2atype.ErrInternalError) || !strings.HasPrefix(err.Error(), apia2a.RuntimeLostMessagePrefix+"Actor team-a/ai-8bd650a8 crashed") {
		t.Fatalf("SendStreamingMessage() error = %v, want the loss", err)
	}
	if len(events) != 3 {
		t.Fatalf("stream events = %#v, want the submitted task, the working status and the failed status", events)
	}
	assertDispatchFailed(t, store, workflow, events, apia2a.RuntimeLostMessagePrefix)
	if len(workflow.marked) != 1 || runtime.getTaskCalls != 0 {
		t.Fatalf("failures recorded = %q, GetTask calls = %d", workflow.marked, runtime.getTaskCalls)
	}
}
