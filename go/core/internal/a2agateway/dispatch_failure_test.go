package a2agateway

import (
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
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
		Metadata: map[string]any{TaskCreatedAtMetadataKey: time.Now().Add(-2 * dispatchGracePeriod).UTC().Format(time.RFC3339Nano)}}
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
