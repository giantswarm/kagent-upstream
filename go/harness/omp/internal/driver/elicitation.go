package driver

import (
	"context"
	"fmt"
	"sync"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/harness/runtime"
)

// Elicitation is an MCP server's URL mode elicitation the adapter's bridge
// holds a tool call of omp's open for: the turn parks on it, and the person's
// answer releases the call.
type Elicitation struct {
	request *runtime.URLElicitationRequest
	answer  chan string
	once    sync.Once
}

// NewElicitation returns an elicitation of request, unanswered.
func NewElicitation(request *runtime.URLElicitationRequest) *Elicitation {
	return &Elicitation{request: request, answer: make(chan string, 1)}
}

// Request is what the person is asked.
func (e *Elicitation) Request() *runtime.URLElicitationRequest { return e.request }

// Answer gives the bridge the person's action, once; a later answer is
// dropped.
func (e *Elicitation) Answer(action string) {
	e.once.Do(func() { e.answer <- action })
}

// Wait returns the person's action, or cancel when ctx ends first: omp
// stopped waiting for the call.
func (e *Elicitation) Wait(ctx context.Context) string {
	select {
	case action := <-e.answer:
		return action
	case <-ctx.Done():
		return apia2a.URLElicitationCancel
	}
}

// pendingTurn is an omp turn parked on an elicitation: omp runs on, its tool
// call held by the bridge until Resume or Cancel answers it.
type pendingTurn struct {
	driver      *ProcessDriver
	session     *processSession
	tracker     *promptTracker
	before      sessionStats
	elicitation *Elicitation
}

func (p *pendingTurn) Request() runtime.InputRequest { return p.elicitation.Request() }

// Resume answers the elicitation and consumes the same omp process until the
// turn completes, fails or parks on another elicitation.
func (p *pendingTurn) Resume(ctx context.Context, response runtime.InputResponse, sink runtime.EventSink) (runtime.Outcome, error) {
	answer, ok := response.(*runtime.URLElicitationResponse)
	if !ok || answer.ID != p.elicitation.Request().ID {
		_ = p.Cancel(ctx)
		return runtime.Outcome{}, fmt.Errorf("the turn waits on URL elicitation %q, not on this response", p.elicitation.Request().ID)
	}
	p.elicitation.Answer(answer.Action)
	outcome, err := p.driver.consumeTurn(ctx, p.session, p.tracker, p.before, sink)
	if err != nil || outcome.Pending == nil {
		p.driver.stop(p.session)
	}
	return outcome, err
}

// Cancel answers the elicitation with cancel, aborts the prompt and stops omp.
func (p *pendingTurn) Cancel(context.Context) error {
	p.elicitation.Answer(apia2a.URLElicitationCancel)
	_, _ = p.session.client.send("abort", "")
	p.driver.stop(p.session)
	return nil
}
