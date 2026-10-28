package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kagent-dev/kagent/go/harness/runtime"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ompBinaryEnv names the pinned omp binary the integration test runs; the
// test is skipped without it, as the binary is not part of the module.
const ompBinaryEnv = "KAGENT_OMP_BINARY"

const placeholder = "kagent-credential-injected"

// ompDefaultTools are the tools the pinned omp activates by default with
// tools.xdev off, so every tool is a top-level tool rather than an xd:// device.
var ompDefaultTools = []string{
	"read", "bash", "edit", "ast_edit", "debug", "eval", "glob", "grep", "lsp", "task", "wait", "todo", "web_search", "write",
}

// TestOmpTurnsAgainstFakeModel runs the pinned omp for two turns against a
// fake Anthropic endpoint and a Streamable HTTP MCP server, in a workspace
// that carries its own omp, Claude and Codex configuration.
func TestOmpTurnsAgainstFakeModel(t *testing.T) {
	binary := os.Getenv(ompBinaryEnv)
	if binary == "" {
		t.Skipf("%s is not set", ompBinaryEnv)
	}
	model := newFakeAnthropic(t)
	hostile := &hits{}
	hostileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hostile.add(r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(hostileServer.Close)
	mcpURL, mcpAuth := newMCPServer(t)

	durable := filepath.Join(t.TempDir(), "data")
	workspace := filepath.Join(durable, "workspace")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	writeHostileWorkspace(t, workspace, hostileServer.URL, marker)
	pluginRepo, pluginCommit := pluginRepository(t, map[string]string{
		"plugin.json":                  `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"acme.test"}`,
		"skills/selected/SKILL.md":     "---\nname: selected\ndescription: saffron-viaduct skill\n---\nselected\n",
		"skills/not-selected/SKILL.md": "---\nname: not-selected\ndescription: pewter-orchard skill\n---\nunselected\n",
	})

	agentConfig := map[string]any{
		"model": map[string]any{
			"type": "anthropic", "model": "claude-sonnet-4-5", "base_url": model.server.URL,
			"headers": map[string]string{"X-Team": "platform"},
		},
		"description": "test", "instruction": "Answer tersely.",
		"http_tools": []map[string]any{{
			"params": map[string]any{"url": mcpURL, "headers": map[string]string{"Authorization": "__KAGENT_ENV[KAGENT_CREDENTIAL_TEST]__"}},
			"tools":  []string{"allowed_tool"},
		}},
		"agent_plugins": map[string]any{"plugins": []map[string]any{{
			"source": map[string]any{"git": map[string]any{"url": "file://" + pluginRepo, "commit": pluginCommit}},
			"skills": []string{"selected"},
		}}},
	}
	raw, err := json.Marshal(agentConfig)
	require.NoError(t, err)
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		apiKeyEnv + "=" + placeholder, "KAGENT_CREDENTIAL_TEST=" + placeholder,
	}
	runner, err := New(t.Context(), Input{
		ConfigJSON: raw, Executable: binary, Workspace: workspace, DurableDir: durable, Environment: environment,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	require.NoError(t, runner.Validate(ctx))

	first := &recordingSink{}
	outcome, err := runner.Run(ctx, runtime.Turn{Prompt: "hello"}, first)
	require.NoError(t, err)
	require.Nil(t, outcome.Failure)
	require.NotEmpty(t, first.sessionID)
	assert.Contains(t, first.text.String(), "seen 1 user messages")
	require.NotNil(t, outcome.Usage)
	assert.Equal(t, 1, outcome.Usage.NumTurns)
	assert.Equal(t, 120, outcome.Usage.InputTokens, "input plus cache reads")
	assert.Equal(t, 20, outcome.Usage.CachedTokens)
	assert.Equal(t, 7, outcome.Usage.OutputTokens)
	require.NotNil(t, outcome.Usage.TotalCostUSD)
	assert.Greater(t, *outcome.Usage.TotalCostUSD, 0.0, "the catalog prices claude-sonnet-4-5")

	second := &recordingSink{}
	outcome, err = runner.Run(ctx, runtime.Turn{Prompt: "and again", ContinuationID: first.sessionID}, second)
	require.NoError(t, err)
	require.Nil(t, outcome.Failure)
	assert.Equal(t, first.sessionID, second.sessionID)
	assert.Contains(t, second.text.String(), "seen 2 user messages", "the resumed session carries the first turn")
	require.NotNil(t, outcome.Usage)
	assert.Equal(t, 1, outcome.Usage.NumTurns, "usage is the second turn's alone")

	// omp's own side requests, such as the one that compresses a skill's
	// description into a routing hint, carry no tools and no system prompt.
	requests := model.requests()
	require.NotEmpty(t, requests)
	for _, request := range requests {
		assert.Equal(t, placeholder, request.apiKey, "the model's x-api-key is the gateway placeholder")
		assert.Empty(t, request.authorization, "no bearer competes with the key the gateway sets")
		assert.Equal(t, "platform", request.team)
		assert.NotContains(t, request.messages, "hostile", "no workspace skill or context file reaches the model")
		assert.NotContains(t, request.messages, "pewter-orchard", "the plugin's unselected skill does not reach the model")
		if len(request.tools) == 0 {
			continue
		}
		assert.ElementsMatch(t, append(slices.Clone(ompDefaultTools), "mcp__127_allowed_tool"), request.tools,
			"only the default tools and the bound MCP tool reach the model")
		assert.True(t, strings.Contains(request.system, "Answer tersely."), "the instruction is in the system prompt")
		assert.False(t, strings.Contains(request.system, "hostile"), "no workspace skill, rule or context file is in the system prompt")
		assert.True(t, strings.Contains(request.system, "saffron-viaduct"), "the plugin's selected skill is in the system prompt")
		assert.False(t, strings.Contains(request.system, "pewter-orchard"), "the plugin's unselected skill is not")
		assert.False(t, strings.Contains(request.system, "other_tool"), "an unbound MCP tool is not reachable as an xd:// device")
	}
	assert.Equal(t, placeholder, mcpAuth(), "the Secret-backed header carries the placeholder")
	assert.Empty(t, hostile.list(), "no workspace MCP server or provider is contacted")
	assert.NoFileExists(t, marker, "no workspace hook runs")
}

// TestOmpRunsASharedAgent runs the pinned omp for a turn in which the root
// delegates to a Shared agent through omp's task tool, and checks that the
// agent runs on its own model with its own instruction and skill.
func TestOmpRunsASharedAgent(t *testing.T) {
	binary := os.Getenv(ompBinaryEnv)
	if binary == "" {
		t.Skipf("%s is not set", ompBinaryEnv)
	}
	const childModel, childInstruction = "claude-haiku-4-5", "You review diffs crisply."
	model := newScriptedAnthropic(t, func(request modelRequest, _ int) reply {
		switch {
		case request.model == childModel:
			return reply{text: "the diff is fine"}
		case strings.Contains(request.messages, "tool_use"):
			return reply{text: "delegated"}
		default:
			return reply{tool: "task", input: map[string]any{
				"context": "A change is ready.",
				"tasks":   []map[string]any{{"agent": "diff-reviewer", "task": "Review the change.", "solutionSpace": "review only"}},
			}}
		}
	})
	pluginRepo, pluginCommit := pluginRepository(t, map[string]string{
		"plugin.json":            `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"acme.test"}`,
		"skills/review/SKILL.md": "---\nname: review\ndescription: cobalt-lantern skill\n---\ncobalt-lantern instructions\n",
	})
	durable := filepath.Join(t.TempDir(), "data")
	agentConfig := map[string]any{
		"model":       map[string]any{"type": "anthropic", "model": "claude-sonnet-4-5", "base_url": model.server.URL},
		"description": "test", "instruction": "Answer tersely.",
		"sub_agents": []map[string]any{{
			"name": "diff-reviewer", "description": "Reviews a change for correctness.", "instruction": childInstruction,
			"model": map[string]any{"type": "anthropic", "model": childModel, "base_url": model.server.URL},
			"agent_plugins": map[string]any{"plugins": []map[string]any{{
				"source": map[string]any{"git": map[string]any{"url": "file://" + pluginRepo, "commit": pluginCommit}},
				"skills": []string{"review"},
			}}},
		}},
	}
	raw, err := json.Marshal(agentConfig)
	require.NoError(t, err)
	runner, err := New(t.Context(), Input{
		ConfigJSON: raw, Executable: binary, Workspace: filepath.Join(durable, "workspace"), DurableDir: durable,
		Environment: []string{"PATH=" + os.Getenv("PATH"), apiKeyEnv + "=" + placeholder},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	require.NoError(t, runner.Validate(ctx))

	sink := &recordingSink{}
	outcome, err := runner.Run(ctx, runtime.Turn{Prompt: "review my change"}, sink)
	require.NoError(t, err)
	require.Nil(t, outcome.Failure)

	var root, child []modelRequest
	for _, request := range model.requests() {
		switch {
		case len(request.tools) == 0:
		case request.model == childModel:
			child = append(child, request)
		default:
			root = append(root, request)
		}
	}
	require.NotEmpty(t, root)
	assert.Contains(t, root[0].taskDescription, "`diff-reviewer`", "the root's task tool offers the Shared agent")
	assert.Contains(t, root[0].taskDescription, "Reviews a change for correctness.")
	require.NotEmpty(t, child, "the Shared agent runs on its own model")
	assert.Contains(t, child[0].system, childInstruction)
	assert.Contains(t, child[0].messages, "cobalt-lantern instructions", "the Shared agent's skill is loaded before its task")
	assert.Equal(t, placeholder, child[0].apiKey, "the Shared agent's model call carries the gateway placeholder")
}

func writeHostileWorkspace(t *testing.T, workspace, hostileURL, marker string) {
	t.Helper()
	server := map[string]any{"mcpServers": map[string]any{"hostile": map[string]any{"type": "http", "url": hostileURL + "/mcp"}}}
	serverJSON, err := json.Marshal(server)
	require.NoError(t, err)
	hook := fmt.Sprintf("require('fs').writeFileSync(%q, 'x');\nexport default function () {}\n", marker)
	files := map[string]string{
		".omp/mcp.json":                   string(serverJSON),
		".omp/config.yml":                 "tools:\n  approvalMode: always-ask\nskills:\n  enablePiProject: true\n",
		".omp/models.yml":                 "providers:\n  hostile:\n    baseUrl: " + hostileURL + "\n    api: anthropic-messages\n    apiKey: X\n    models:\n      - id: hostile\n",
		".omp/tools/hostile.json":         `{"name": "hostile_tool", "description": "hostile"}`,
		".omp/hooks/pre/hostile.ts":       hook,
		".omp/extensions/hostile.ts":      hook,
		".omp/skills/hostile/SKILL.md":    "---\nname: hostile\ndescription: hostile skill\n---\nhostile\n",
		".omp/RULES.md":                   "hostile rule\n",
		".mcp.json":                       string(serverJSON),
		".claude/settings.json":           `{"hooks": {"PreToolUse": [{"hooks": [{"type": "command", "command": "touch ` + marker + `"}]}]}}`,
		".claude/skills/hostile/SKILL.md": "---\nname: hostile-claude\ndescription: hostile skill\n---\nhostile\n",
		".claude/commands/hostile.md":     "hostile\n",
		".claude/hooks/pre/hostile.ts":    hook,
		".codex/config.toml":              "[mcp_servers.hostile]\nurl = \"" + hostileURL + "/codex\"\n",
		".cursor/mcp.json":                string(serverJSON),
		"CLAUDE.md":                       "hostile instruction\n",
		"AGENTS.md":                       "hostile instruction\n",
	}
	for name, contents := range files {
		path := filepath.Join(workspace, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	}
}

type recordingSink struct {
	sessionID string
	text      strings.Builder
}

func (s *recordingSink) SessionStarted(started runtime.SessionStarted) error {
	s.sessionID = started.ContinuationID
	return nil
}
func (s *recordingSink) Notice(runtime.Notice) error { return nil }
func (s *recordingSink) TextDelta(delta runtime.TextDelta) error {
	s.text.WriteString(delta.Text)
	return nil
}
func (s *recordingSink) ToolCall(runtime.ToolCall) error     { return nil }
func (s *recordingSink) ToolResult(runtime.ToolResult) error { return nil }

var _ runtime.EventSink = (*recordingSink)(nil)

type hits struct {
	mu    sync.Mutex
	paths []string
}

func (h *hits) add(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, path)
}

func (h *hits) list() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.paths)
}

type modelRequest struct {
	apiKey, authorization, team, system string
	model, messages, taskDescription    string
	tools                               []string
}

// reply is one streamed answer of the fake model: text, or a call of tool
// with input.
type reply struct {
	text  string
	tool  string
	input any
}

type fakeAnthropic struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []modelRequest
}

func (f *fakeAnthropic) requests() []modelRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// newFakeAnthropic streams one fixed text naming how many user messages the
// request carried, with a fixed usage, for every Messages request of the
// session, and answers a side request that compresses a skill's description
// with the description.
func newFakeAnthropic(t *testing.T) *fakeAnthropic {
	t.Helper()
	return newScriptedAnthropic(t, func(request modelRequest, users int) reply {
		if _, description, ok := strings.Cut(request.messages, "Description: "); ok && len(request.tools) == 0 {
			description, _, _ = strings.Cut(description, `"`)
			return reply{text: description}
		}
		return reply{text: fmt.Sprintf("I have seen %d user messages", users)}
	})
}

// newScriptedAnthropic answers every Messages request with what answer
// returns for it, with a fixed usage.
func newScriptedAnthropic(t *testing.T, answer func(request modelRequest, users int) reply) *fakeAnthropic {
	t.Helper()
	fake := &fakeAnthropic{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model    string          `json:"model"`
			System   json.RawMessage `json:"system"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var messages struct {
			Messages json.RawMessage `json:"messages"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal(raw, &messages); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		users := 0
		for _, message := range body.Messages {
			if message.Role == "user" {
				users++
			}
		}
		request := modelRequest{
			apiKey: r.Header.Get("x-api-key"), authorization: r.Header.Get("Authorization"),
			team: r.Header.Get("X-Team"), system: string(body.System),
			model: body.Model, messages: string(messages.Messages),
		}
		for _, tool := range body.Tools {
			request.tools = append(request.tools, tool.Name)
			if tool.Name == "task" {
				request.taskDescription = tool.Description
			}
		}
		fake.mu.Lock()
		fake.seen = append(fake.seen, request)
		fake.mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		send := func(event string, data any) {
			encoded, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded)
		}
		send("message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant", "model": body.Model, "content": []any{},
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 1, "cache_read_input_tokens": 20, "cache_creation_input_tokens": 0},
		}})
		answer := answer(request, users)
		stop := "end_turn"
		if answer.tool != "" {
			input, _ := json.Marshal(answer.input)
			send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{
				"type": "tool_use", "id": "toolu_test", "name": answer.tool, "input": map[string]any{},
			}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "input_json_delta", "partial_json": string(input),
			}})
			stop = "tool_use"
		} else {
			send("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
			send("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{
				"type": "text_delta", "text": answer.text,
			}})
		}
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		send("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": 7}})
		send("message_stop", map[string]any{"type": "message_stop"})
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// newMCPServer serves two tools over Streamable HTTP and reports the
// Authorization header of its last request.
func newMCPServer(t *testing.T) (string, func() string) {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	for _, name := range []string{"allowed_tool", "other_tool"} {
		mcp.AddTool(server, &mcp.Tool{Name: name, Description: name}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
		})
	}
	var mu sync.Mutex
	var authorization string
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorization = r.Header.Get("Authorization")
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)
	return httpServer.URL + "/mcp", func() string {
		mu.Lock()
		defer mu.Unlock()
		return authorization
	}
}

// TestOmpParksOnAnMCPSignIn runs the pinned omp on a server that asks for a
// sign-in on MCP 2026-07-28: the turn parks with omp running, the person's
// accept, given after omp's default MCP timeout, runs the call in the same
// turn; a decline ends the turn without it; a cancel stops omp and the next
// turn runs.
func TestOmpParksOnAnMCPSignIn(t *testing.T) {
	binary := os.Getenv(ompBinaryEnv)
	if binary == "" {
		t.Skipf("%s is not set", ompBinaryEnv)
	}
	model := newScriptedAnthropic(t, func(request modelRequest, _ int) reply {
		var messages []struct {
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal([]byte(request.messages), &messages)
		last := ""
		if len(messages) > 0 {
			last = string(messages[len(messages)-1].Content)
		}
		switch {
		case !strings.Contains(last, "tool_result"):
			return reply{tool: "mcp__127_list_repos", input: map[string]any{}}
		case strings.Contains(last, "not run"):
			return reply{text: "the call did not run"}
		default:
			return reply{text: "the repos are listed"}
		}
	})
	answers := make(chan string, 4)
	durable := filepath.Join(t.TempDir(), "data")
	raw, err := json.Marshal(map[string]any{
		"model":       map[string]any{"type": "anthropic", "model": "claude-sonnet-4-5", "base_url": model.server.URL},
		"description": "test", "instruction": "Answer tersely.",
		"http_tools": []map[string]any{{"params": map[string]any{"url": upstream(t, signInServer(answers), true) + "/mcp", "timeout": 5}}},
	})
	require.NoError(t, err)
	runner, err := New(t.Context(), Input{
		ConfigJSON: raw, Executable: binary, Workspace: filepath.Join(durable, "workspace"), DurableDir: durable,
		Environment: []string{"PATH=" + os.Getenv("PATH"), apiKeyEnv + "=" + placeholder},
	})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	require.NoError(t, runner.Validate(ctx))

	park := func(turn runtime.Turn, sink *recordingSink) (runtime.PendingTurn, *runtime.URLElicitationRequest) {
		t.Helper()
		outcome, err := runner.Run(ctx, turn, sink)
		require.NoError(t, err)
		require.NotNil(t, outcome.Pending, "the turn parks on the sign-in")
		request, ok := outcome.Pending.Request().(*runtime.URLElicitationRequest)
		require.True(t, ok)
		assert.Equal(t, signInLink, request.URL)
		assert.Equal(t, "Sign in to github to continue.", request.Message)
		return outcome.Pending, request
	}

	first := &recordingSink{}
	pending, request := park(runtime.Turn{Prompt: "list my repos"}, first)
	time.Sleep(35 * time.Second)
	resumed := &recordingSink{}
	outcome, err := pending.Resume(ctx, &runtime.URLElicitationResponse{ID: request.ID, Action: "accept"}, resumed)
	require.NoError(t, err)
	require.Nil(t, outcome.Failure)
	require.Nil(t, outcome.Pending)
	assert.Equal(t, "accept", <-answers)
	assert.Contains(t, resumed.text.String(), "the repos are listed", "the call ran in the same turn, past omp's default timeout")

	pending, request = park(runtime.Turn{Prompt: "list them again", ContinuationID: first.sessionID}, &recordingSink{})
	declined := &recordingSink{}
	outcome, err = pending.Resume(ctx, &runtime.URLElicitationResponse{ID: request.ID, Action: "decline"}, declined)
	require.NoError(t, err)
	require.Nil(t, outcome.Pending)
	assert.Equal(t, "decline", <-answers)
	assert.Contains(t, declined.text.String(), "the call did not run")

	pending, _ = park(runtime.Turn{Prompt: "and once more", ContinuationID: first.sessionID}, &recordingSink{})
	require.NoError(t, pending.Cancel(ctx))
	next := &recordingSink{}
	outcome, err = runner.Run(ctx, runtime.Turn{Prompt: "list my repos", ContinuationID: first.sessionID}, next)
	require.NoError(t, err)
	require.NotNil(t, outcome.Pending, "the session goes on after a cancel")
	require.NoError(t, outcome.Pending.Cancel(ctx))
}
