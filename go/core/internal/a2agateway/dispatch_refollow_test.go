package a2agateway

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"testing"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv/eventqueue"
	"github.com/stretchr/testify/require"
)

// refollowRuntime holds a working task whose dispatch stream ended early, and
// streams it again on a resubscription until released, then completes it.
type refollowRuntime struct {
	gatewayTestRuntime
	resubscribed atomic.Int32
	release      chan struct{}
}

func (r *refollowRuntime) SubscribeToTask(context.Context, a2aclient.ServiceParams, *a2atype.SubscribeToTaskRequest) iter.Seq2[a2atype.Event, error] {
	r.resubscribed.Add(1)
	return func(yield func(a2atype.Event, error) bool) {
		if !yield(r.task, nil) {
			return
		}
		<-r.release
		completed := *r.task
		completed.Status.State = a2atype.TaskStateCompleted
		yield(&completed, nil)
	}
}

func refollowGateway() (*Gateway, *gatewayTestStore) {
	store := &gatewayTestStore{instance: gatewayTestInstance()}
	return &Gateway{store: store, workflow: &gatewayTestWorkflow{}, events: eventqueue.NewInMemoryManager(), coordinator: &memoryRuntimeCoordinator{}}, store
}

// workingStream reports the task working, then ends with err, or cleanly when
// err is nil.
func workingStream(task *a2atype.Task, err error) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		if !yield(a2atype.NewStatusUpdateEvent(task, a2atype.TaskStateWorking, nil), nil) {
			return
		}
		if err != nil {
			yield(nil, err)
		}
	}
}

func workingTask() *a2atype.Task {
	return &a2atype.Task{ID: "active", ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: a2atype.TaskStateWorking}}
}

func TestDispatchFollowsItsTaskAgainUntilItEnds(t *testing.T) {
	for name, streamErr := range map[string]error{
		"a cut stream":             errors.New("stream cut by an idle timeout"),
		"a stream that ends early": nil,
	} {
		t.Run(name, func(t *testing.T) {
			gateway, store := refollowGateway()
			task := workingTask()
			runtime := &refollowRuntime{gatewayTestRuntime: gatewayTestRuntime{task: task}, release: make(chan struct{})}
			run, _, err := gateway.startTaskRun(gatewayTestContext(), store.instance, task, gatewayTestClient(t, runtime), workingStream(task, streamErr), true)
			require.NoError(t, err)

			require.Eventually(t, func() bool { return runtime.resubscribed.Load() == 1 }, 5*time.Second, 10*time.Millisecond)
			select {
			case <-run.done:
				t.Fatal("the run ended while the runtime still ran its task")
			default:
			}

			close(runtime.release)
			<-run.done
			require.NoError(t, run.err)
			require.Equal(t, a2atype.TaskStateCompleted, store.task.Status.State)
		})
	}
}

func TestDispatchEndsWhenItsTaskCannotBeFollowedAgain(t *testing.T) {
	gateway, store := refollowGateway()
	task := workingTask()
	runtime := &gatewayTestRuntime{task: task}
	run, _, err := gateway.startTaskRun(gatewayTestContext(), store.instance, task, gatewayTestClient(t, runtime), workingStream(task, errors.New("stream cut by an idle timeout")), true)
	require.NoError(t, err)

	<-run.done
	require.ErrorContains(t, run.err, "stream cut by an idle timeout", "the run reports the stream's own failure")
	require.Equal(t, maxDispatchResubscribes, runtime.subscribeCalls)
}

func TestObservingRunEndsWithItsStream(t *testing.T) {
	gateway, store := refollowGateway()
	task := workingTask()
	runtime := &gatewayTestRuntime{task: task}
	run, _, err := gateway.startTaskRun(gatewayTestContext(), store.instance, task, gatewayTestClient(t, runtime), workingStream(task, nil), false)
	require.NoError(t, err)

	<-run.done
	require.NoError(t, run.err)
	require.Zero(t, runtime.subscribeCalls, "only the run that dispatched a turn follows it again")
}
