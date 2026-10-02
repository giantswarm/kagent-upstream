package a2agateway

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sync"
	"sync/atomic"
	"time"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv/eventqueue"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
)

// taskRun is the single owner of task event persistence and runtime quiescence.
// Public streams only observe the events it publishes.
type taskRun struct {
	gateway   *Gateway
	client    *a2aclient.Client
	closeOnce sync.Once
	closeErr  error
	key       string
	queueID   a2atype.TaskID
	done      chan struct{}
	// dispatch marks a run that delivers the task to the runtime, as opposed to
	// one that observes a task already there.
	dispatch bool
	// canceling marks a run whose task a caller asked to cancel. The runtime
	// may end the stream with an error instead of a final CANCELED event when
	// the cancel races the turn's start; that error settles the task CANCELED.
	canceling atomic.Bool

	mu   sync.Mutex
	err  error
	last a2atype.Event
	// task is the projection of the last event ingested: what a caller that waited
	// for the run is handed.
	task *a2atype.Task
}

func taskRunKey(instanceID string, taskID a2atype.TaskID) string {
	return instanceID + "/" + string(taskID)
}

func (g *Gateway) taskRun(instanceID string, taskID a2atype.TaskID) (*taskRun, bool) {
	run, ok := g.runs.Load(taskRunKey(instanceID, taskID))
	if !ok {
		return nil, false
	}
	return run.(*taskRun), true
}

func (g *Gateway) startTaskRun(ctx context.Context, instance *apiv1alpha1.AgentInstance, task *a2atype.Task, client *a2aclient.Client, events iter.Seq2[a2atype.Event, error], dispatch bool) (*taskRun, eventqueue.Reader, error) {
	key := taskRunKey(instance.GetId(), task.ID)
	run := &taskRun{gateway: g, client: client, key: key, queueID: a2atype.TaskID(key), done: make(chan struct{}), dispatch: dispatch, task: task}
	if _, loaded := g.runs.LoadOrStore(key, run); loaded {
		return nil, nil, fmt.Errorf("task event ingester already exists")
	}
	writer, err := g.events.CreateWriter(ctx, run.queueID)
	if err != nil {
		g.runs.Delete(key)
		return nil, nil, fmt.Errorf("create task event publisher: %w", err)
	}
	reader, err := g.events.CreateReader(ctx, run.queueID)
	if err != nil {
		_ = writer.Close()
		_ = g.events.Destroy(ctx, run.queueID)
		g.runs.Delete(key)
		return nil, nil, fmt.Errorf("create task event reader: %w", err)
	}
	go run.ingest(context.WithoutCancel(ctx), instance, task, writer, events)
	return run, reader, nil
}

// runtimeHoldsTask asks the runtime whether it took a task whose dispatch
// stream failed before reporting it. Only a runtime that answers with the task
// is left to finish it; one that does not know the task, or cannot be asked,
// has left a turn no client could end.
func (r *taskRun) runtimeHoldsTask(ctx context.Context, task *a2atype.Task) bool {
	latest, err := r.client.GetTask(ctx, &a2atype.GetTaskRequest{ID: task.ID})
	return err == nil && latest != nil
}

// Cancellation and terminal ingestion can both close ingress. Share the
// result so grpc.ClientConn.Close is called exactly once.
func (r *taskRun) closeRuntime() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.client.Destroy()
	})
	return r.closeErr
}

func (r *taskRun) ingest(ctx context.Context, instance *apiv1alpha1.AgentInstance, task *a2atype.Task, writer eventqueue.Writer, events iter.Seq2[a2atype.Event, error]) {
	defer func() {
		_ = writer.Close()
		_ = r.closeRuntime()
		// Unregister before signalling done: a caller that waited for the run
		// and looks the task up again must not find it.
		r.gateway.runs.CompareAndDelete(r.key, r)
		close(r.done)
		_ = r.gateway.events.Destroy(ctx, r.queueID)
	}()

	resubscribes := 0
	var cause error
	for {
		progressed, done, eventErr := r.follow(ctx, instance, &task, writer, events)
		if done {
			return
		}
		if cause == nil {
			cause = eventErr
		}
		// A stream that fails on a runtime that is gone — its Actor crashed
		// or no longer exists — ends the turn: nothing is going to finish
		// it, and asking the runtime would only wait out another refusal.
		if failed, lostErr := r.gateway.failLostRuntime(ctx, instance, task, eventErr); lostErr != nil {
			r.publishFailure(ctx, writer, failed, task)
			r.setError(lostErr)
			return
		}
		// A stream that ends while its task is being cancelled ended because
		// of the cancel: the turn is over as the caller asked, not failed.
		if r.canceling.Load() {
			_, _ = r.ingestEvent(ctx, instance, task, writer, a2atype.NewStatusUpdateEvent(task, a2atype.TaskStateCanceled, nil))
			return
		}
		if progressed {
			resubscribes = 0
		}
		// A dispatch whose stream fails or ends early may have lost only the stream. A
		// runtime that answers for the task is still running the turn: follow
		// it again, so the turn ends when the task does and not when a stream
		// is cut. One that does not know a submitted task never started the
		// turn: leave a failed task, not a submitted one, and let observers see
		// it before the error.
		if r.dispatch {
			holds := r.runtimeHoldsTask(ctx, task)
			if holds && resubscribes < maxDispatchResubscribes {
				resubscribes++
				if !sleepContext(ctx, resubscribeBackoff(resubscribes)) {
					r.setError(cause)
					return
				}
				events = subscribeTask(ctx, r.client, &a2atype.SubscribeToTaskRequest{ID: task.ID})
				continue
			}
			if !holds && task.Status.State == a2atype.TaskStateSubmitted {
				r.publishFailure(ctx, writer, r.gateway.recordTaskFailure(ctx, instance, task, cause), task)
			}
		}
		r.setError(cause)
		return
	}
}

// errStreamEndedEarly is how a dispatching run's stream that ended before its
// task quiesced fails: a runtime that still reports the task is followed again,
// as after a cut stream.
var errStreamEndedEarly = errors.New("runtime ended the task's stream before the task quiesced")

// maxDispatchResubscribes bounds how many times in a row a dispatching run
// follows its task again without the runtime reporting anything new.
const maxDispatchResubscribes = 3

func resubscribeBackoff(attempt int) time.Duration {
	return time.Duration(attempt) * 100 * time.Millisecond
}

func sleepContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// follow ingests one runtime stream into the task. It reports whether the
// stream carried an event beyond the task's current state (a resubscription
// opens with that state), whether the run is over (the task quiesced, or
// ingestion failed), and the error that ended the stream otherwise. A run that
// observes a task is also over when its stream ends; a dispatching run's stream
// that ends first fails with errStreamEndedEarly.
func (r *taskRun) follow(ctx context.Context, instance *apiv1alpha1.AgentInstance, task **a2atype.Task, writer eventqueue.Writer, events iter.Seq2[a2atype.Event, error]) (progressed, done bool, err error) {
	seen := 0
	for event, eventErr := range events {
		if eventErr != nil {
			return seen > 1, false, eventErr
		}
		updated, ok := r.ingestEvent(ctx, instance, *task, writer, event)
		if !ok {
			return false, true, nil
		}
		*task = updated
		seen++
		if isQuiescent(updated.Status.State) {
			return false, true, nil
		}
	}
	if r.dispatch {
		return seen > 1, false, errStreamEndedEarly
	}
	return false, true, nil
}

// ingestEvent persists one event of the task and publishes it to observers. It
// returns the task the event produced, or false once the run has recorded the
// error that ends it.
func (r *taskRun) ingestEvent(ctx context.Context, instance *apiv1alpha1.AgentInstance, task *a2atype.Task, writer eventqueue.Writer, event a2atype.Event) (*a2atype.Task, bool) {
	updated, err := taskForEvent(task, event)
	if err == nil && isQuiescent(updated.Status.State) {
		release := r.gateway.coordinator.Quiesce(instance.GetId())
		// Terminal suspension must close the runtime stream so it cannot wait on
		// itself. Input pauses checkpoint the still-live request first.
		if updated.Status.State.Terminal() {
			if closeErr := r.closeRuntime(); closeErr != nil {
				err = fmt.Errorf("close terminal runtime stream: %w", closeErr)
			}
		}
		if err == nil {
			err = r.gateway.storeEvent(ctx, instance, updated, event)
		}
		release()
	} else if err == nil {
		err = r.gateway.storeEvent(ctx, instance, updated, event)
	}
	if err != nil {
		r.setError(r.gateway.storeError(ctx, err))
		return nil, false
	}
	// Record the projection before publishing: a caller returning on this
	// event reads the task the event produced.
	r.setLast(event, updated)
	if err := writer.Write(ctx, &eventqueue.Message{Event: event}); err != nil {
		r.setError(r.gateway.storeError(ctx, fmt.Errorf("publish task event: %w", err)))
		return nil, false
	}
	return updated, true
}

// publishFailure lets observers see a recorded failure before the error that
// follows it. A failure that could not be recorded is nil and publishes nothing.
func (r *taskRun) publishFailure(ctx context.Context, writer eventqueue.Writer, failed a2atype.Event, task *a2atype.Task) {
	if failed == nil {
		return
	}
	if err := writer.Write(ctx, &eventqueue.Message{Event: failed}); err == nil {
		r.setLast(failed, task)
	}
}

// await follows the run on behalf of a caller that wants the turn's outcome
// rather than its events: the task once it quiesces, or the message the runtime
// answered with. A caller that asked to return immediately gets the task as soon
// as the runtime reported it, as a non-streaming send does. The caller's context
// bounds only the wait: a caller that gives up gets the task as recorded so far
// with its context's error while the run keeps ingesting, so the turn still lands
// in the task for whoever reads it next.
func (r *taskRun) await(ctx context.Context, reader eventqueue.Reader, returnImmediately bool) (a2atype.SendMessageResult, error) {
	var err error
	for event, readErr := range r.observeReader(ctx, nil, reader) {
		if readErr != nil {
			err = readErr
			break
		}
		if _, message := event.(*a2atype.Message); returnImmediately && !message {
			return r.getTask(), nil
		}
	}
	if ctx.Err() == nil {
		// The queue closed because the run is ending: let it finish, so the
		// caller returns to a released runtime and a settled task.
		<-r.done
	}
	if err != nil {
		return r.getTask(), err
	}
	if message, ok := r.getLast().(*a2atype.Message); ok {
		return message, nil
	}
	return r.getTask(), nil
}

func (r *taskRun) observe(ctx context.Context, initial a2atype.Event) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		select {
		case <-r.done:
			if event := r.getLast(); event != nil {
				if !yield(event, nil) {
					return
				}
			} else if initial != nil {
				if !yield(initial, nil) {
					return
				}
			}
			if err := r.getError(); err != nil {
				yield(nil, err)
			}
			return
		default:
		}
		reader, err := r.gateway.events.CreateReader(ctx, r.queueID)
		if err != nil {
			yield(nil, err)
			return
		}
		for event, err := range r.observeReader(ctx, initial, reader) {
			if !yield(event, err) {
				return
			}
		}
	}
}

func (r *taskRun) observeReader(ctx context.Context, initial a2atype.Event, reader eventqueue.Reader) iter.Seq2[a2atype.Event, error] {
	return func(yield func(a2atype.Event, error) bool) {
		defer reader.Close()
		if initial != nil && !yield(initial, nil) {
			return
		}
		received := false
		for {
			message, err := reader.Read(ctx)
			if errors.Is(err, eventqueue.ErrQueueClosed) {
				if !received {
					if event := r.getLast(); event != nil && !yield(event, nil) {
						return
					}
				}
				if runErr := r.getError(); runErr != nil {
					yield(nil, runErr)
				}
				return
			}
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(message.Event, nil) {
				return
			}
			received = true
		}
	}
}

func (r *taskRun) setError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.err = err
}

func (r *taskRun) getError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *taskRun) setLast(event a2atype.Event, task *a2atype.Task) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last, r.task = event, task
}

func (r *taskRun) getTask() *a2atype.Task {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.task
}

func (r *taskRun) getLast() a2atype.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}
