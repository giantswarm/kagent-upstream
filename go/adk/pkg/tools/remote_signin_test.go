package tools

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/stretchr/testify/require"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool/toolconfirmation"

	"github.com/kagent-dev/kagent/go/adk/pkg/a2a"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
)

const childSignInURL = "https://gateway.example/oauth/proxy/start?s=1"

// childAgent is a sub-agent that pauses its first turn on a URL elicitation
// and completes the turn that answers it.
type childAgent struct {
	a2asrv.RequestHandler

	// url is the link of the child's request; empty means childSignInURL.
	url string

	mu       sync.Mutex
	answers  []*a2atype.Message
	canceled []a2atype.TaskID
}

func (c *childAgent) SendMessage(_ context.Context, request *a2atype.SendMessageRequest) (a2atype.SendMessageResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if request.Message.TaskID == "" {
		link := c.url
		if link == "" {
			link = childSignInURL
		}
		status := a2a.AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("Sign in to github to continue.")), &apia2a.URLElicitationRequest{
			Type: apia2a.HITLTypeURLElicitationRequest, ID: "child-sign-in", Server: "github",
			Message: "Open " + link, URL: link, ElicitationID: "e1",
		})
		return &a2atype.Task{ID: "child-task", ContextID: "child-context", Status: a2atype.TaskStatus{
			State: a2atype.TaskStateInputRequired, Message: status,
		}}, nil
	}
	c.answers = append(c.answers, request.Message)
	return &a2atype.Task{ID: request.Message.TaskID, ContextID: "child-context",
		Status:    a2atype.TaskStatus{State: a2atype.TaskStateCompleted},
		Artifacts: []*a2atype.Artifact{{Parts: a2atype.ContentParts{a2atype.NewTextPart("jane")}}},
	}, nil
}

func (c *childAgent) CancelTask(_ context.Context, request *a2atype.CancelTaskRequest) (*a2atype.Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.canceled = append(c.canceled, request.ID)
	return &a2atype.Task{ID: request.ID, ContextID: "child-context", Status: a2atype.TaskStatus{State: a2atype.TaskStateCanceled}}, nil
}

// signInToolContext is the tool context of the parent's call to the child.
type signInToolContext struct {
	adkagent.StrictContextMock
	confirmation *toolconfirmation.ToolConfirmation
	hint         string
	payload      any
	requested    bool
}

func (c *signInToolContext) UserID() string    { return "jane" }
func (c *signInToolContext) SessionID() string { return "parent-context" }
func (c *signInToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return c.confirmation
}

func (c *signInToolContext) RequestConfirmation(hint string, payload any) error {
	c.hint, c.payload, c.requested = hint, payload, true
	return nil
}

// parentCall returns the context of a tool call in a turn whose caller
// activated the HITL extension, or did not.
func parentCall(t *testing.T, activated bool, confirmation *toolconfirmation.ToolConfirmation) *signInToolContext {
	t.Helper()
	ctx, callCtx := a2asrv.NewCallContext(t.Context(), nil)
	if activated {
		extension := apia2a.HITLExtension()
		callCtx.Extensions().Activate(&extension)
	}
	return &signInToolContext{StrictContextMock: adkagent.NewStrictContextMock(ctx), confirmation: confirmation}
}

func newChildTool(t *testing.T, child *childAgent) *remoteA2AState {
	t.Helper()
	server := httptest.NewServer(a2asrv.NewJSONRPCHandler(child))
	t.Cleanup(server.Close)
	client, err := a2aclient.NewFromEndpoints(t.Context(), []*a2atype.AgentInterface{{
		URL: server.URL, ProtocolBinding: a2atype.TransportProtocolJSONRPC, ProtocolVersion: a2atype.Version,
	}}, a2aclient.WithJSONRPCTransport(server.Client()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Destroy()) })
	state := &remoteA2AState{name: "worker"}
	state.initOnce.Do(func() { state.a2aClient = client })
	return state
}

func TestRemoteSignInIsRelayedToACallerWithTheHITLExtension(t *testing.T) {
	child := &childAgent{}
	state := newChildTool(t, child)

	first := parentCall(t, true, nil)
	response, err := state.run(first, "who am I?")
	require.NoError(t, err)
	require.Equal(t, "pending", response.Status)
	require.True(t, first.requested)
	require.Equal(t, "Remote agent 'worker' needs the person to sign in to github.", first.hint)
	payload, ok := first.payload.(map[string]any)
	require.True(t, ok, "payload = %#v", first.payload)
	paused := a2a.ParseRemoteHitlState(payload)
	require.NotNil(t, paused)
	require.NotNil(t, paused.URLElicitationRequest)
	require.Equal(t, childSignInURL, paused.URLElicitationRequest.URL)

	paused.URLElicitationResponse = &apia2a.URLElicitationResponse{
		Type: apia2a.HITLTypeURLElicitationResponse, ID: "child-sign-in", Action: apia2a.URLElicitationAccept,
	}
	resumed := parentCall(t, true, &toolconfirmation.ToolConfirmation{Confirmed: true, Payload: paused.ToMap()})
	response, err = state.run(resumed, "")
	require.NoError(t, err)
	require.Equal(t, "jane", response.Result)

	require.Len(t, child.answers, 1)
	answer := child.answers[0]
	require.Equal(t, a2atype.TaskID("child-task"), answer.TaskID)
	forwarded := a2a.GetURLElicitationResponse(answer)
	require.NotNil(t, forwarded)
	require.Equal(t, "child-sign-in", forwarded.ID)
	require.Equal(t, apia2a.URLElicitationAccept, forwarded.Action)
	require.Empty(t, child.canceled)
}

func TestRemoteSignInIsRefusedForACallerWithoutTheHITLExtension(t *testing.T) {
	child := &childAgent{}
	state := newChildTool(t, child)

	call := parentCall(t, false, nil)
	response, err := state.run(call, "who am I?")
	require.NoError(t, err)
	require.Equal(t, "failed", response.Status)
	require.Contains(t, response.Error, "github")
	require.NotContains(t, response.Error, childSignInURL)
	require.False(t, call.requested, "the parent's turn does not pause")
	require.Equal(t, []a2atype.TaskID{"child-task"}, child.canceled, "the child does not wait for an answer that cannot come")
}

func TestRemoteSignInWithAnUnsafeURLCancelsTheChildsTask(t *testing.T) {
	const unsafe = "javascript:alert(1)"
	child := &childAgent{url: unsafe}
	state := newChildTool(t, child)

	call := parentCall(t, true, nil)
	response, err := state.run(call, "who am I?")
	require.NoError(t, err)
	require.Equal(t, "failed", response.Status)
	require.NotContains(t, response.Error, unsafe)
	require.False(t, call.requested, "the parent's turn does not pause")
	require.Equal(t, []a2atype.TaskID{"child-task"}, child.canceled, "the child does not wait for an answer that cannot come")
}
