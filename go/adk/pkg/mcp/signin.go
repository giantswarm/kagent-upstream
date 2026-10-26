package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
)

// SignInPayloadKey is the key of the tool confirmation payload that carries
// a URL elicitation, such as a sign-in link, an MCP server asked the person
// for during a tool call.
const SignInPayloadKey = "kagent_url_elicitation"

// SignIn is a URL elicitation an MCP server sent during a tool call.
type SignIn struct {
	Server        string `json:"server"`
	Message       string `json:"message,omitempty"`
	URL           string `json:"url"`
	ElicitationID string `json:"elicitation_id,omitempty"`
}

// signInRecorder holds the URL elicitation of one tool call.
type signInRecorder struct {
	mu     sync.Mutex
	signIn *SignIn
}

func (r *signInRecorder) record(signIn SignIn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.signIn == nil {
		r.signIn = &signIn
	}
}

func (r *signInRecorder) take() *SignIn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.signIn
}

type signInRecorderKey struct{}

// recorderContext is a tool context whose MCP requests carry the recorder of
// the tool call.
type recorderContext struct {
	adkagent.Context
	recorder *signInRecorder
}

func (c recorderContext) Value(key any) any {
	if key == (signInRecorderKey{}) {
		return c.recorder
	}
	return c.Context.Value(key)
}

// newElicitingClient returns the MCP client of one server. It declares form
// and URL elicitation. A URL elicitation that comes during a tool call is
// recorded on that call and canceled, so the call ends without running and
// the tool asks the person instead; the MCP SDK answers a server on MCP
// 2026-07-28 through this handler. A form, or an elicitation outside a tool
// call, is declined: what the person typed into a form would reach the
// model's context.
func newElicitingClient(serverURL string) *mcpsdk.Client {
	return mcpsdk.NewClient(&mcpsdk.Implementation{Name: "kagent-adk"}, &mcpsdk.ClientOptions{
		Capabilities: &mcpsdk.ClientCapabilities{
			Elicitation: &mcpsdk.ElicitationCapabilities{Form: &mcpsdk.FormElicitationCapabilities{}, URL: &mcpsdk.URLElicitationCapabilities{}},
		},
		ElicitationHandler: elicitationHandler(serverURL),
	})
}

func elicitationHandler(serverURL string) func(context.Context, *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
	return func(ctx context.Context, request *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
		recorder, _ := ctx.Value(signInRecorderKey{}).(*signInRecorder)
		if recorder == nil || request.Params.Mode != "url" || request.Params.URL == "" {
			return &mcpsdk.ElicitResult{Action: apia2a.URLElicitationDecline}, nil
		}
		recorder.record(SignIn{
			Server: serverName(request.Session, serverURL), Message: request.Params.Message,
			URL: request.Params.URL, ElicitationID: request.Params.ElicitationID,
		})
		return &mcpsdk.ElicitResult{Action: apia2a.URLElicitationCancel}, nil
	}
}

// serverName names an MCP server for the person: the name it reported, or
// its host.
func serverName(session *mcpsdk.ClientSession, serverURL string) string {
	if session != nil {
		if result := session.InitializeResult(); result != nil && result.ServerInfo != nil && result.ServerInfo.Name != "" {
			return result.ServerInfo.Name
		}
	}
	if parsed, err := url.Parse(serverURL); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return serverURL
}

// urlElicitationRequired reads the URL elicitation of a -32042 error, the
// answer of a server on MCP 2025-11-25.
func urlElicitationRequired(err error, serverURL string) *SignIn {
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != mcpsdk.CodeURLElicitationRequired || len(rpcErr.Data) == 0 {
		return nil
	}
	var data struct {
		Elicitations []mcpsdk.ElicitParams `json:"elicitations"`
	}
	if json.Unmarshal(rpcErr.Data, &data) != nil {
		return nil
	}
	for _, elicitation := range data.Elicitations {
		if elicitation.Mode == "url" && elicitation.URL != "" {
			return &SignIn{
				Server: serverName(nil, serverURL), Message: elicitation.Message,
				URL: elicitation.URL, ElicitationID: elicitation.ElicitationID,
			}
		}
	}
	return nil
}

// signInToolset turns a URL elicitation of its server during a tool call
// into a confirmation of that call that carries it: the run pauses until the
// person signed in, then the confirmed call runs again.
type signInToolset struct {
	inner     tool.Toolset
	serverURL string
}

func (s *signInToolset) Name() string { return s.inner.Name() }

func (s *signInToolset) Tools(ctx adkagent.ReadonlyContext) ([]tool.Tool, error) {
	tools, err := s.inner.Tools(ctx)
	if err != nil {
		return nil, err
	}
	wrapped := make([]tool.Tool, 0, len(tools))
	for _, inner := range tools {
		if runnable, ok := inner.(runnableTool); ok {
			wrapped = append(wrapped, &signInTool{runnableTool: runnable, serverURL: s.serverURL})
			continue
		}
		wrapped = append(wrapped, inner)
	}
	return wrapped, nil
}

type runnableTool interface {
	tool.Tool
	Declaration() *genai.FunctionDeclaration
	Run(ctx adkagent.Context, args any) (map[string]any, error)
	ProcessRequest(ctx adkagent.Context, req *model.LLMRequest) error
}

type signInTool struct {
	runnableTool
	serverURL string
}

// ProcessRequest packs the inner tool and dispatches its calls to this one.
func (t *signInTool) ProcessRequest(ctx adkagent.Context, req *model.LLMRequest) error {
	if err := t.runnableTool.ProcessRequest(ctx, req); err != nil {
		return err
	}
	if req.Tools != nil && req.Tools[t.Name()] != nil {
		req.Tools[t.Name()] = t
	}
	return nil
}

func (t *signInTool) Run(ctx adkagent.Context, args any) (map[string]any, error) {
	recorder := &signInRecorder{}
	result, err := t.runnableTool.Run(recorderContext{Context: ctx, recorder: recorder}, args)
	signIn := recorder.take()
	if signIn == nil {
		signIn = urlElicitationRequired(err, t.serverURL)
	}
	if signIn == nil {
		return result, err
	}
	hint := signIn.Message
	if hint == "" {
		hint = fmt.Sprintf("Sign in to %s to continue.", signIn.Server)
	}
	if err := ctx.RequestConfirmation(hint, map[string]any{SignInPayloadKey: signIn}); err != nil {
		return nil, err
	}
	ctx.Actions().SkipSummarization = true
	return nil, fmt.Errorf("error tool %q %w", t.Name(), tool.ErrConfirmationRequired)
}
