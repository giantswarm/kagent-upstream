package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/harness/omp/internal/config"
	"github.com/kagent-dev/kagent/go/harness/omp/internal/driver"
	"github.com/kagent-dev/kagent/go/harness/runtime"
	"github.com/kagent-dev/kagent/go/harness/workspace"
	"github.com/kagent-dev/kagent/go/pkg/logging"
)

const bridgeConnectTimeout = 30 * time.Second

// startBridges serves every server on loopback and returns the URL omp
// reaches each one at. The bridge is omp's MCP client towards the server: it
// calls the server with the binding's headers (a Secret-backed header stays
// the placeholder the egress gateway replaces; on one of the turn's
// actor-JWT hosts, a server without an Authorization of its own is called
// with the placeholder the gateway replaces with the Actor's JWT), serves omp
// only the tools the binding selects, and declares URL mode elicitation, so a
// sign-in link the server asks the person to open reaches them as the turn's
// input request and never the model.
func startBridges(ctx context.Context, servers []config.MCPServer, actorJWT *turnActorJWTHosts, elicitations chan<- *driver.Elicitation) (map[string]string, error) {
	mux := http.NewServeMux()
	urls := map[string]string{}
	var bridges []*mcpBridge
	for _, server := range servers {
		bridge := newMCPBridge(ctx, server, actorJWT, elicitations)
		bridges = append(bridges, bridge)
		path := "/" + server.Name
		mux.Handle(path, mcp.NewStreamableHTTPHandler(bridge.serverFor, nil))
		urls[server.Name] = path
	}
	if len(urls) == 0 {
		return urls, nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen for the MCP bridge: %w", err)
	}
	for name, path := range urls {
		urls[name] = "http://" + listener.Addr().String() + path
	}
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.FromContext(ctx).ErrorContext(ctx, "mcp bridge stopped", "error", err)
		}
	}()
	// The bridges live as long as the adapter's context: the process.
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
		for _, bridge := range bridges {
			bridge.close()
		}
	}()
	return urls, nil
}

// mcpBridge holds one connection to its server, opened when omp first
// connects and opened again when the server has closed it, and serves omp the
// server's tools, resources, resource templates and prompts as the server
// lists them.
type mcpBridge struct {
	ctx          context.Context
	upstream     config.MCPServer
	client       *mcp.Client
	http         *http.Client
	elicitations chan<- *driver.Elicitation

	mu      sync.Mutex
	session *mcp.ClientSession
	server  *mcp.Server

	// served is what the server is serving, by kind, so a list change
	// removes what the upstream server no longer lists.
	syncMu sync.Mutex
	served map[string]map[string]bool
}

func newMCPBridge(ctx context.Context, upstream config.MCPServer, actorJWT *turnActorJWTHosts, elicitations chan<- *driver.Elicitation) *mcpBridge {
	b := &mcpBridge{
		ctx: ctx, upstream: upstream, elicitations: elicitations,
		http: &http.Client{Transport: headerTransport{
			url: upstream.URL, headers: upstream.Headers, actorJWT: actorJWT, base: http.DefaultTransport,
		}},
		served: map[string]map[string]bool{},
	}
	b.client = mcp.NewClient(&mcp.Implementation{Name: "kagent-omp", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{
			Elicitation: &mcp.ElicitationCapabilities{URL: &mcp.URLElicitationCapabilities{}},
		},
		ElicitationHandler:         b.elicit,
		ToolListChangedHandler:     func(context.Context, *mcp.ToolListChangedRequest) { b.resync() },
		ResourceListChangedHandler: func(context.Context, *mcp.ResourceListChangedRequest) { b.resync() },
		PromptListChangedHandler:   func(context.Context, *mcp.PromptListChangedRequest) { b.resync() },
	})
	return b
}

// serverFor answers omp's sessions with what the server lists. Without a
// connection there is no server: omp reports the server unavailable and the
// next turn tries again.
func (b *mcpBridge) serverFor(*http.Request) *mcp.Server {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server != nil {
		return b.server
	}
	session, err := b.connectLocked()
	if err != nil {
		logging.FromContext(b.ctx).ErrorContext(b.ctx, "mcp bridge cannot reach its server", "server", b.upstream.Name, "error", err)
		return nil
	}
	server := mcp.NewServer(&mcp.Implementation{Name: b.upstream.Name, Version: "1"}, &mcp.ServerOptions{
		Capabilities: mirroredCapabilities(session.InitializeResult()),
	})
	if err := b.sync(session, server); err != nil {
		logging.FromContext(b.ctx).ErrorContext(b.ctx, "mcp bridge cannot list its server", "server", b.upstream.Name, "error", err)
		return nil
	}
	b.server = server
	return server
}

// mirroredCapabilities are the capabilities omp is served: tools always, and
// resources and prompts when the upstream server has them, each announcing
// its list changes, which the bridge relays.
func mirroredCapabilities(initialized *mcp.InitializeResult) *mcp.ServerCapabilities {
	capabilities := &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}}
	if initialized == nil || initialized.Capabilities == nil {
		return capabilities
	}
	if initialized.Capabilities.Resources != nil {
		capabilities.Resources = &mcp.ResourceCapabilities{ListChanged: true}
	}
	if initialized.Capabilities.Prompts != nil {
		capabilities.Prompts = &mcp.PromptCapabilities{ListChanged: true}
	}
	return capabilities
}

// resync serves what the server lists after it announced a list change.
func (b *mcpBridge) resync() {
	b.mu.Lock()
	session, server := b.session, b.server
	b.mu.Unlock()
	if session == nil || server == nil {
		return
	}
	if err := b.sync(session, server); err != nil {
		logging.FromContext(b.ctx).ErrorContext(b.ctx, "mcp bridge cannot list its server after a change", "server", b.upstream.Name, "error", err)
	}
}

const (
	servedTools     = "tools"
	servedResources = "resources"
	servedTemplates = "templates"
	servedPrompts   = "prompts"
)

// sync serves the server's selected tools (all of them when the binding
// selects none), its resources, resource templates and prompts, and stops
// serving what it no longer lists.
func (b *mcpBridge) sync(session *mcp.ClientSession, server *mcp.Server) error {
	b.syncMu.Lock()
	defer b.syncMu.Unlock()
	ctx, cancel := context.WithTimeout(b.ctx, bridgeConnectTimeout)
	defer cancel()
	selected := make(map[string]bool, len(b.upstream.Tools))
	for _, tool := range b.upstream.Tools {
		selected[tool] = true
	}
	tools := map[string]bool{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return fmt.Errorf("list tools: %w", err)
		}
		if len(selected) == 0 || selected[tool.Name] {
			server.AddTool(tool, b.call)
			tools[tool.Name] = true
		}
	}
	b.prune(servedTools, tools, server.RemoveTools)
	capabilities := session.InitializeResult().Capabilities
	if capabilities != nil && capabilities.Resources != nil {
		resources := map[string]bool{}
		for resource, err := range session.Resources(ctx, nil) {
			if err != nil {
				return fmt.Errorf("list resources: %w", err)
			}
			server.AddResource(resource, b.readResource)
			resources[resource.URI] = true
		}
		b.prune(servedResources, resources, server.RemoveResources)
		templates := map[string]bool{}
		for template, err := range session.ResourceTemplates(ctx, nil) {
			if err != nil {
				return fmt.Errorf("list resource templates: %w", err)
			}
			server.AddResourceTemplate(template, b.readResource)
			templates[template.URITemplate] = true
		}
		b.prune(servedTemplates, templates, server.RemoveResourceTemplates)
	}
	if capabilities != nil && capabilities.Prompts != nil {
		prompts := map[string]bool{}
		for prompt, err := range session.Prompts(ctx, nil) {
			if err != nil {
				return fmt.Errorf("list prompts: %w", err)
			}
			server.AddPrompt(prompt, b.getPrompt)
			prompts[prompt.Name] = true
		}
		b.prune(servedPrompts, prompts, server.RemovePrompts)
	}
	return nil
}

// prune removes what was served of kind and is no longer listed, and records
// what is listed now.
func (b *mcpBridge) prune(kind string, listed map[string]bool, remove func(...string)) {
	var gone []string
	for name := range b.served[kind] {
		if !listed[name] {
			gone = append(gone, name)
		}
	}
	if len(gone) > 0 {
		remove(gone...)
	}
	b.served[kind] = listed
}

// call forwards one call of a served tool. The binding's timeout bounds the
// call, except while the person decides on a URL elicitation of it. A server
// on MCP 2025-11-25 answers a call that needs a sign-in with -32042: the
// person is asked the same way and, once they accepted, the call runs once
// more.
func (b *mcpBridge) call(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ctx, deadline := withCallDeadline(ctx, time.Duration(b.upstream.TimeoutSeconds*float64(time.Second)))
	defer deadline.stop()
	params := &mcp.CallToolParams{Name: request.Params.Name, Arguments: request.Params.Arguments}
	result, err := b.callTool(ctx, params)
	elicitation := urlElicitationRequired(err)
	if elicitation == nil {
		return result, err
	}
	if action := b.ask(ctx, elicitation); action != apia2a.URLElicitationAccept {
		return notRun(b.upstream.Name, action), nil
	}
	return b.callTool(ctx, params)
}

func (b *mcpBridge) callTool(ctx context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	session, err := b.connection()
	if err != nil {
		return nil, err
	}
	result, err := session.CallTool(ctx, params)
	if errors.Is(err, mcp.ErrConnectionClosed) {
		if session, err = b.reconnect(session); err != nil {
			return nil, err
		}
		result, err = session.CallTool(ctx, params)
	}
	return result, err
}

func (b *mcpBridge) readResource(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	session, err := b.connection()
	if err != nil {
		return nil, err
	}
	return session.ReadResource(ctx, &mcp.ReadResourceParams{URI: request.Params.URI})
}

func (b *mcpBridge) getPrompt(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	session, err := b.connection()
	if err != nil {
		return nil, err
	}
	return session.GetPrompt(ctx, &mcp.GetPromptParams{Name: request.Params.Name, Arguments: request.Params.Arguments})
}

// elicit answers the server's elicitation during a tool call (MCP
// 2026-07-28 asks through the call's input requests). A URL elicitation goes
// to the person; a form is declined, as what the person typed would reach
// the model; an elicitation outside a tool call has no turn to park and is
// declined too.
func (b *mcpBridge) elicit(ctx context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
	if request.Params.Mode != "url" || callDeadlineFrom(ctx) == nil {
		return &mcp.ElicitResult{Action: apia2a.URLElicitationDecline}, nil
	}
	return &mcp.ElicitResult{Action: b.ask(ctx, request.Params)}, nil
}

// ask parks the turn on a URL elicitation and returns the person's action:
// decline for a link that is not https, cancel when omp stops waiting. The
// call's deadline does not run while the person decides.
func (b *mcpBridge) ask(ctx context.Context, params *mcp.ElicitParams) string {
	link, err := url.Parse(params.URL)
	if err != nil || link.Scheme != "https" || link.Host == "" {
		return apia2a.URLElicitationDecline
	}
	if deadline := callDeadlineFrom(ctx); deadline != nil {
		deadline.pause()
		defer deadline.resume()
	}
	elicitation := driver.NewElicitation(&runtime.URLElicitationRequest{
		ID: uuid.NewString(), Server: b.upstream.Name, Message: params.Message,
		URL: params.URL, ElicitationID: params.ElicitationID,
	})
	select {
	case b.elicitations <- elicitation:
	case <-ctx.Done():
		return apia2a.URLElicitationCancel
	}
	return elicitation.Wait(ctx)
}

// urlElicitationRequired reads the URL elicitation of a -32042 error, the
// answer of a server on MCP 2025-11-25 to a call that needs the person.
func urlElicitationRequired(err error) *mcp.ElicitParams {
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != mcp.CodeURLElicitationRequired || len(rpcErr.Data) == 0 {
		return nil
	}
	var data struct {
		Elicitations []mcp.ElicitParams `json:"elicitations"`
	}
	if json.Unmarshal(rpcErr.Data, &data) != nil {
		return nil
	}
	for _, elicitation := range data.Elicitations {
		if elicitation.Mode == "url" && elicitation.URL != "" {
			return &elicitation
		}
	}
	return nil
}

// notRun is the result of a call the person did not let run.
func notRun(server, action string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("The tool did not run: the person answered %s to %s's request.", action, server)}},
	}
}

func (b *mcpBridge) connection() (*mcp.ClientSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session != nil {
		return b.session, nil
	}
	return b.connectLocked()
}

// reconnect replaces a closed connection unless another call already has.
func (b *mcpBridge) reconnect(closed *mcp.ClientSession) (*mcp.ClientSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session != closed {
		return b.session, nil
	}
	b.session = nil
	return b.connectLocked()
}

func (b *mcpBridge) connectLocked() (*mcp.ClientSession, error) {
	if b.session != nil {
		return b.session, nil
	}
	ctx, cancel := context.WithTimeout(b.ctx, bridgeConnectTimeout)
	defer cancel()
	session, err := b.client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: b.upstream.URL, HTTPClient: b.http}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect to MCP server %q: %w", b.upstream.Name, err)
	}
	b.session = session
	return session, nil
}

func (b *mcpBridge) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.session != nil {
		_ = b.session.Close()
		b.session = nil
	}
}

// callDeadline is a tool call's timeout that stops while the person decides
// on an elicitation of the call. Zero is no timeout.
type callDeadline struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	timer   *time.Timer
	left    time.Duration
	started time.Time
	paused  int
}

type callDeadlineKey struct{}

func withCallDeadline(ctx context.Context, timeout time.Duration) (context.Context, *callDeadline) {
	ctx, cancel := context.WithCancel(ctx)
	deadline := &callDeadline{cancel: cancel, left: timeout, started: time.Now()}
	if timeout > 0 {
		deadline.timer = time.AfterFunc(timeout, cancel)
	}
	return context.WithValue(ctx, callDeadlineKey{}, deadline), deadline
}

func callDeadlineFrom(ctx context.Context) *callDeadline {
	deadline, _ := ctx.Value(callDeadlineKey{}).(*callDeadline)
	return deadline
}

func (d *callDeadline) pause() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused++
	if d.paused == 1 && d.timer != nil && d.timer.Stop() {
		d.left -= time.Since(d.started)
	}
}

func (d *callDeadline) resume() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.paused--
	if d.paused == 0 && d.timer != nil {
		d.started = time.Now()
		d.timer.Reset(max(d.left, 0))
	}
}

func (d *callDeadline) stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
	}
	d.cancel()
}

// headerTransport sets a binding's headers on every request to its server,
// and the placeholder Authorization while the server takes the current
// turn's Actor JWT.
type headerTransport struct {
	url      string
	headers  map[string]string
	actorJWT *turnActorJWTHosts
	base     http.RoundTripper
}

func (t headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	for name, value := range workspace.ActorJWTHeaders(t.url, t.headers, t.actorJWT.get()) {
		request.Header.Set(name, value)
	}
	return t.base.RoundTrip(request)
}

// turnActorJWTHosts holds the actor-JWT hosts of the turn omp runs. The
// bridges start with the adapter, and the hosts, the Session's, reach it only
// with each turn's call.
type turnActorJWTHosts struct {
	hosts atomic.Pointer[[]string]
}

func (h *turnActorJWTHosts) set(hosts []string) { h.hosts.Store(&hosts) }

func (h *turnActorJWTHosts) get() []string {
	if hosts := h.hosts.Load(); hosts != nil {
		return *hosts
	}
	return nil
}
