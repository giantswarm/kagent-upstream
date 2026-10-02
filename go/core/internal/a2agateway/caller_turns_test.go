package a2agateway

import (
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv/eventqueue"
	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/core/internal/callercredential"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
)

func TestDispatchedRunHoldsTheCallersTurnUntilItEnds(t *testing.T) {
	for _, test := range []struct {
		name     string
		dispatch bool
		final    a2atype.TaskState
	}{
		{name: "completed", dispatch: true, final: a2atype.TaskStateCompleted},
		{name: "parked for input", dispatch: true, final: a2atype.TaskStateInputRequired},
		{name: "observing run", dispatch: false, final: a2atype.TaskStateCompleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			turns := callercredential.NewTurns()
			instance := gatewayTestInstance()
			instance.A2AAuthority = substrate.ActorHost("team-a", "ai-"+gatewayTestID, "")
			actor, err := substrate.ActorTargetFromHost(instance.A2AAuthority)
			require.NoError(t, err)
			store := &gatewayTestStore{instance: instance}
			gateway := &Gateway{store: store, workflow: &gatewayTestWorkflow{}, events: eventqueue.NewInMemoryManager(), coordinator: &memoryRuntimeCoordinator{}, turns: turns}
			task := &a2atype.Task{ID: "active", ContextID: gatewayTestContextID, Status: a2atype.TaskStatus{State: a2atype.TaskStateWorking}}
			release := make(chan struct{})
			events := func(yield func(a2atype.Event, error) bool) {
				<-release
				yield(a2atype.NewStatusUpdateEvent(task, test.final, nil), nil)
			}
			run, _, err := gateway.startTaskRun(gatewayTestContext(), instance, task, gatewayTestClient(t, &singleCloseGatewayRuntime{}), events, test.dispatch)
			require.NoError(t, err)

			_, running := turns.Current(actor)
			require.Equal(t, test.dispatch, running, "only a run that dispatches the turn acts for its caller")
			close(release)
			<-run.done
			_, running = turns.Current(actor)
			require.False(t, running, "a run that stopped following its task leaves no turn behind")
		})
	}
}
