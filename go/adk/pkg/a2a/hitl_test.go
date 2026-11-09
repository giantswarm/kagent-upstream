package a2a

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	kagentmcp "github.com/kagent-dev/kagent/go/adk/pkg/mcp"
	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"google.golang.org/adk/v2/tool/toolconfirmation"
)

func dataPart(data map[string]any, metadata map[string]any) *a2atype.Part {
	part := a2atype.NewDataPart(data)
	part.Metadata = metadata
	return part
}

func confirmationPart(id, toolName, toolID string, args, payload map[string]any) *a2atype.Part {
	return dataPart(map[string]any{
		"name": toolconfirmation.FunctionCallName,
		"id":   id,
		"args": map[string]any{
			"originalFunctionCall": map[string]any{"name": toolName, "id": toolID, "args": args},
			"toolConfirmation":     map[string]any{"hint": "Please confirm", "payload": payload},
		},
	}, map[string]any{"adk_type": "function_call", "adk_is_long_running": true})
}

func hintedConfirmationPart(id, toolName, toolID, hint string) *a2atype.Part {
	return dataPart(map[string]any{
		"name": toolconfirmation.FunctionCallName,
		"id":   id,
		"args": map[string]any{
			"originalFunctionCall": map[string]any{"name": toolName, "id": toolID},
			"toolConfirmation":     map[string]any{"hint": hint},
		},
	}, map[string]any{"adk_type": "function_call", "adk_is_long_running": true})
}

func hintlessConfirmationPart(id, toolName, toolID string) *a2atype.Part {
	return dataPart(map[string]any{
		"name": toolconfirmation.FunctionCallName,
		"id":   id,
		"args": map[string]any{
			"originalFunctionCall": map[string]any{"name": toolName, "id": toolID},
		},
	}, map[string]any{"adk_type": "function_call", "adk_is_long_running": true})
}

func askUserConfirmationPart(id, toolID string, questions []any, hint string) *a2atype.Part {
	return dataPart(map[string]any{
		"name": toolconfirmation.FunctionCallName,
		"id":   id,
		"args": map[string]any{
			"originalFunctionCall": map[string]any{
				"name": "ask_user", "id": toolID, "args": map[string]any{"questions": questions},
			},
			"toolConfirmation": map[string]any{"hint": hint},
		},
	}, map[string]any{"adk_type": "function_call", "adk_is_long_running": true})
}

func messageText(message *a2atype.Message) string {
	var text strings.Builder
	for _, part := range message.Parts {
		if content, ok := part.Content.(a2atype.Text); ok {
			text.WriteString(string(content))
		}
	}
	return text.String()
}

func hitlDecisionMessage(payload any) *a2atype.Message {
	message := a2atype.NewMessage(a2atype.MessageRoleUser, a2atype.NewTextPart("Decision"))
	return AttachHitlExtension(message, payload)
}

func TestHitlExtensionAttachAndParse(t *testing.T) {
	message := hitlDecisionMessage(&apia2a.ToolApprovalResponse{
		Type:      HITLTypeToolApprovalResponse,
		Approvals: []apia2a.ToolApproval{{ID: "confirm-1", Approved: true}},
	})
	payload := GetToolApprovalResponse(message)
	if payload == nil || len(payload.Approvals) != 1 || !payload.Approvals[0].Approved {
		t.Fatalf("unexpected HITL payload: %#v", payload)
	}
}

func TestHitlExtensionRejectsEmptyApprovals(t *testing.T) {
	message := AttachHitlExtension(
		a2atype.NewMessage(a2atype.MessageRoleUser, a2atype.NewTextPart("Decision")),
		map[string]any{"type": HITLTypeToolApprovalResponse, "approvals": []any{}},
	)
	if GetToolApprovalResponse(message) != nil {
		t.Fatal("empty approvals should not decode")
	}
}

func TestHitlExtensionDoesNotReadLegacyDataPart(t *testing.T) {
	legacy := a2atype.NewMessage(a2atype.MessageRoleUser, a2atype.NewDataPart(map[string]any{"decision_type": "approve"}))
	if IsHITLResponse(legacy) {
		t.Fatal("legacy DataPart was treated as a HITL response")
	}
}

func TestHITLActivationInterceptor(t *testing.T) {
	ctx, callCtx := a2asrv.NewCallContext(context.Background(), a2asrv.NewServiceParams(map[string][]string{
		a2atype.SvcParamExtensions: {HITLExtensionURI},
	}))
	if _, _, err := HITLActivationInterceptor().Before(ctx, callCtx, &a2asrv.Request{}); err != nil {
		t.Fatalf("Before() error = %v", err)
	}
	if !apia2a.HITLActive(ctx) {
		t.Fatal("HITL was not activated")
	}
}

func TestBuildHITLStatusMessage(t *testing.T) {
	t.Run("tool approval", func(t *testing.T) {
		internal := a2atype.NewMessage(a2atype.MessageRoleAgent,
			confirmationPart("confirm-1", "delete_file", "call-1", map[string]any{"path": "/tmp/x"}, nil))
		payload := GetToolApprovalRequest(BuildHITLStatusMessage(internal, true))
		if payload == nil || payload.Tools[0].ID != "confirm-1" {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("ask user", func(t *testing.T) {
		questions := []any{map[string]any{
			"question": "Which databases?", "choices": []any{"PostgreSQL", "MySQL"}, "multiple": true,
		}}
		internal := a2atype.NewMessage(a2atype.MessageRoleAgent,
			confirmationPart("confirm-2", "ask_user", "call-2", map[string]any{"questions": questions}, nil))
		payload := GetAskUserRequest(BuildHITLStatusMessage(internal, true))
		if payload == nil || len(payload.Questions) != 1 || payload.Questions[0].Question != "Which databases?" ||
			!slices.Equal(payload.Questions[0].Choices, []string{"PostgreSQL", "MySQL"}) || !payload.Questions[0].Multiple {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("ask user without questions asks for approval", func(t *testing.T) {
		internal := a2atype.NewMessage(a2atype.MessageRoleAgent,
			hintlessConfirmationPart("confirm-2", "ask_user", "call-2"))
		public := BuildHITLStatusMessage(internal, true)
		if GetAskUserRequest(public) != nil {
			t.Fatal("an ask_user call without questions must not become an ask_user request")
		}
		payload := GetToolApprovalRequest(public)
		if payload == nil || payload.Tools[0].Name != "ask_user" {
			t.Fatalf("payload = %#v", payload)
		}
		if want := "Approval is required for tool(s): ask_user"; messageText(public) != want {
			t.Errorf("text = %q, want %q", messageText(public), want)
		}
	})

	t.Run("ask user drops questions without text", func(t *testing.T) {
		questions := []any{map[string]any{"question": ""}, map[string]any{"question": "Which cluster?"}}
		internal := a2atype.NewMessage(a2atype.MessageRoleAgent,
			confirmationPart("confirm-2", "ask_user", "call-2", map[string]any{"questions": questions}, nil))
		public := BuildHITLStatusMessage(internal, true)
		payload := GetAskUserRequest(public)
		if payload == nil || len(payload.Questions) != 1 || payload.Questions[0].Question != "Which cluster?" {
			t.Fatalf("payload = %#v", payload)
		}
		if want := "Which cluster?"; messageText(public) != want {
			t.Errorf("text = %q, want %q", messageText(public), want)
		}
	})

	t.Run("nested subagent", func(t *testing.T) {
		remote := RemoteHitlState{
			TaskID: "child-task", ContextID: "child-context", SubagentName: "k8s_agent",
			ToolApprovalRequest: &apia2a.ToolApprovalRequest{
				Type: HITLTypeToolApprovalRequest,
				Tools: []apia2a.HITLTool{{
					ID: "child-confirm", CallID: "child-call", Name: "delete_pod",
					Args: map[string]any{"name": "api"},
				}},
			},
		}
		payload := GetToolApprovalRequest(BuildHITLStatusMessage(
			a2atype.NewMessage(a2atype.MessageRoleAgent,
				confirmationPart("parent-confirm", "k8s_agent", "parent-call", nil, remote.ToMap())),
			true,
		))
		if payload == nil || payload.Nested == nil || payload.Nested.Tools[0].CallID != "child-call" {
			t.Fatalf("payload = %#v", payload)
		}
	})

	t.Run("not activated", func(t *testing.T) {
		public := BuildHITLStatusMessage(a2atype.NewMessage(a2atype.MessageRoleAgent,
			confirmationPart("confirm-1", "delete_file", "call-1", nil, nil)), false)
		if GetToolApprovalRequest(public) != nil {
			t.Fatalf("unexpected payload on inactive client")
		}
		if want := "Please confirm (delete_file)"; messageText(public) != want {
			t.Errorf("text = %q, want %q", messageText(public), want)
		}
	})

	t.Run("not activated without a tool hint names the tool", func(t *testing.T) {
		public := BuildHITLStatusMessage(a2atype.NewMessage(a2atype.MessageRoleAgent,
			hintlessConfirmationPart("confirm-1", "delete_file", "call-1")), false)
		if want := "Approval is required for tool(s): delete_file"; messageText(public) != want {
			t.Errorf("text = %q, want %q", messageText(public), want)
		}
	})

	t.Run("several tools keep every hint and every name", func(t *testing.T) {
		internal := a2atype.NewMessage(a2atype.MessageRoleAgent,
			confirmationPart("confirm-1", "delete_file", "call-1", nil, nil),
			hintlessConfirmationPart("confirm-2", "restart_pod", "call-2"))
		want := "Please confirm (delete_file, restart_pod)"
		if text := messageText(BuildHITLStatusMessage(internal, false)); text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})

	t.Run("hints join in tool order", func(t *testing.T) {
		internal := a2atype.NewMessage(a2atype.MessageRoleAgent,
			hintedConfirmationPart("confirm-1", "delete_file", "call-1", "First hint"),
			hintedConfirmationPart("confirm-2", "restart_pod", "call-2", "Second hint"))
		want := "First hint; Second hint (delete_file, restart_pod)"
		if text := messageText(BuildHITLStatusMessage(internal, false)); text != want {
			t.Errorf("text = %q, want %q", text, want)
		}
	})

	t.Run("activated payload hint repeats the text", func(t *testing.T) {
		public := BuildHITLStatusMessage(a2atype.NewMessage(a2atype.MessageRoleAgent,
			hintlessConfirmationPart("confirm-1", "delete_file", "call-1")), true)
		payload := GetToolApprovalRequest(public)
		if payload == nil {
			t.Fatal("payload = nil, want a tool approval request")
		}
		if payload.Hint != messageText(public) {
			t.Errorf("hint = %q, text = %q, want them identical", payload.Hint, messageText(public))
		}
	})

	t.Run("not activated ask_user carries the question", func(t *testing.T) {
		questions := []any{map[string]any{"question": "Which database?"}}
		internal := a2atype.NewMessage(a2atype.MessageRoleAgent,
			askUserConfirmationPart("confirm-2", "call-2", questions, "Which database?"))
		public := BuildHITLStatusMessage(internal, false)
		if GetAskUserRequest(public) != nil {
			t.Fatal("unexpected payload on inactive client")
		}
		if want := "Which database?"; messageText(public) != want {
			t.Errorf("text = %q, want the question verbatim %q", messageText(public), want)
		}
	})

	t.Run("non-confirmation long-running call", func(t *testing.T) {
		public := BuildHITLStatusMessage(a2atype.NewMessage(a2atype.MessageRoleAgent,
			dataPart(map[string]any{"name": "auth_required"}, map[string]any{
				"adk_type": "function_call", "adk_is_long_running": true,
			})), true)
		if GetToolApprovalRequest(public) != nil {
			t.Fatal("non-confirmation long-running call produced HITL payload")
		}
	})
}

func TestBuildResumeHITLMessageAskUser(t *testing.T) {
	incoming := hitlDecisionMessage(&apia2a.AskUserResponse{
		Type: HITLTypeAskUserResponse, ID: "confirm-1",
		Answers: []apia2a.AskUserAnswer{{Answer: []string{"PostgreSQL"}}},
	})
	stored := &a2atype.Task{Status: a2atype.TaskStatus{
		State: a2atype.TaskStateInputRequired,
		Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("Answer required")), &apia2a.AskUserRequest{
			Type: HITLTypeAskUserRequest, ID: "confirm-1",
		}),
	}}
	resume, err := BuildResumeHITLMessage(stored, incoming)
	if err != nil || len(resume.Parts) != 1 {
		t.Fatalf("resume = %#v, err = %v", resume, err)
	}
}

func TestBuildResumeHITLMessageBatchFlattensApprovals(t *testing.T) {
	stored := &a2atype.Task{Status: a2atype.TaskStatus{
		State: a2atype.TaskStateInputRequired,
		Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("Approval required")), &apia2a.ToolApprovalRequest{
			Type: HITLTypeToolApprovalRequest,
			Tools: []apia2a.HITLTool{
				{ID: "confirm-1", CallID: "call-1", Name: "delete_file", Args: map[string]any{}},
				{ID: "confirm-2", CallID: "call-2", Name: "restart_pod", Args: map[string]any{}},
			},
		}),
	}}
	incoming := hitlDecisionMessage(&apia2a.ToolApprovalResponse{
		Type: HITLTypeToolApprovalResponse,
		Approvals: []apia2a.ToolApproval{
			{ID: "confirm-1", Approved: true},
			{ID: "confirm-2", Approved: false, RejectionReason: "not now"},
		},
	})
	resume, err := BuildResumeHITLMessage(stored, incoming)
	if err != nil || len(resume.Parts) != 2 {
		t.Fatalf("resume parts = %v, err = %v", len(resume.Parts), err)
	}
}

func TestBuildResumeHITLMessageNestedAskUser(t *testing.T) {
	stored := &a2atype.Task{Status: a2atype.TaskStatus{
		State: a2atype.TaskStateInputRequired,
		Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("Answer required")), &apia2a.AskUserRequest{
			Type: HITLTypeAskUserRequest, ID: "parent-confirm",
			Questions: []apia2a.HITLQuestion{{Question: "Which namespace?"}},
			Nested: &apia2a.NestedHITLRequest{
				TaskID: "child-task", ContextID: "child-context", SubagentName: "child",
				Tools: []apia2a.HITLTool{{ID: "child-confirm", CallID: "child-call", Name: "ask_user", Args: map[string]any{}}},
			},
		}),
	}}
	incoming := hitlDecisionMessage(&apia2a.AskUserResponse{
		Type: HITLTypeAskUserResponse, ID: "child-confirm",
		Answers: []apia2a.AskUserAnswer{{Answer: []string{"default"}}},
	})
	resume, err := BuildResumeHITLMessage(stored, incoming)
	if err != nil {
		t.Fatalf("BuildResumeHITLMessage() error = %v", err)
	}
	response := asDataPart(resume.Parts[0])[PartKeyResponse].(map[string]any)["response"].(string)
	var confirmation toolconfirmation.ToolConfirmation
	if err := json.Unmarshal([]byte(response), &confirmation); err != nil {
		t.Fatalf("confirmation JSON: %v", err)
	}
	state := ParseRemoteHitlState(confirmation.Payload.(map[string]any))
	if state == nil || state.AskUserResponse == nil || len(state.AskUserResponse.Answers) != 1 {
		t.Fatalf("nested confirmation payload = %#v", confirmation.Payload)
	}
}

func TestBuildResumeHITLMessageNestedApprovals(t *testing.T) {
	stored := &a2atype.Task{Status: a2atype.TaskStatus{
		State: a2atype.TaskStateInputRequired,
		Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("Approval required")), &apia2a.ToolApprovalRequest{
			Type:  HITLTypeToolApprovalRequest,
			Tools: []apia2a.HITLTool{{ID: "parent-confirm", CallID: "parent-call", Name: "child", Args: map[string]any{}}},
			Nested: &apia2a.NestedHITLRequest{
				TaskID: "child-task", ContextID: "child-context", SubagentName: "child",
				Tools: []apia2a.HITLTool{
					{ID: "child-confirm-1", CallID: "child-call-1", Name: "delete_pod", Args: map[string]any{}},
					{ID: "child-confirm-2", CallID: "child-call-2", Name: "restart_pod", Args: map[string]any{}},
				},
			},
		}),
	}}
	incoming := hitlDecisionMessage(&apia2a.ToolApprovalResponse{
		Type: HITLTypeToolApprovalResponse,
		Approvals: []apia2a.ToolApproval{
			{ID: "child-confirm-1", Approved: true},
			{ID: "child-confirm-2", Approved: false, RejectionReason: "not now"},
		},
	})
	resume, err := BuildResumeHITLMessage(stored, incoming)
	if err != nil {
		t.Fatalf("BuildResumeHITLMessage() error = %v", err)
	}
	response := asDataPart(resume.Parts[0])[PartKeyResponse].(map[string]any)["response"].(string)
	var confirmation toolconfirmation.ToolConfirmation
	_ = json.Unmarshal([]byte(response), &confirmation)
	state := ParseRemoteHitlState(confirmation.Payload.(map[string]any))
	if confirmation.Confirmed || state == nil || state.ToolApprovalResponse == nil ||
		state.ToolApprovalResponse.Approvals[1].RejectionReason != "not now" {
		t.Fatalf("nested confirmation = %#v", confirmation)
	}
}

func TestBuildRemoteHitlStateAndHint(t *testing.T) {
	task := &a2atype.Task{
		ID: "child-task", ContextID: "child-context",
		Status: a2atype.TaskStatus{
			Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("pause")), &apia2a.ToolApprovalRequest{
				Type: HITLTypeToolApprovalRequest,
				Tools: []apia2a.HITLTool{{
					ID: "child-confirm", CallID: "child-call", Name: "delete_pod", Args: map[string]any{},
				}},
			}),
		},
	}
	state := BuildRemoteHitlState(task, "k8s_agent")
	if state == nil || state.ToolApprovalRequest == nil {
		t.Fatalf("state = %#v", state)
	}
	if got := RemoteHitlHint(state); got != "Remote agent 'k8s_agent' requires approval for tool(s): delete_pod" {
		t.Fatalf("hint = %q", got)
	}
}

// A sub-agent's ask_user pause should surface the question, not just the tool name.
func TestBuildRemoteHitlStateAndHintAskUser(t *testing.T) {
	task := &a2atype.Task{
		ID: "child-task", ContextID: "child-context",
		Status: a2atype.TaskStatus{
			Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("pause")), &apia2a.AskUserRequest{
				Type: HITLTypeAskUserRequest,
				ID:   "confirm-1",
				Questions: []apia2a.HITLQuestion{
					{Question: "What is the GitHub owner/org for the repo?"},
				},
			}),
		},
	}
	state := BuildRemoteHitlState(task, "github_agent")
	if state == nil || state.AskUserRequest == nil {
		t.Fatalf("state = %#v", state)
	}
	want := "Remote agent 'github_agent' asks: What is the GitHub owner/org for the repo?"
	if got := RemoteHitlHint(state); got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
}

// A two-level nested ask_user pause should also surface the question, since
// the nested apia2a.HITLTool's Args round-trip through JSON and lose their type.
func TestBuildRemoteHitlStateAndHintAskUserNested(t *testing.T) {
	question := "What is the GitHub owner/org for the repo?"
	task := &a2atype.Task{
		ID: "child-task", ContextID: "child-context",
		Status: a2atype.TaskStatus{
			Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent, a2atype.NewTextPart("pause")), &apia2a.AskUserRequest{
				Type:      HITLTypeAskUserRequest,
				ID:        "confirm-1",
				Questions: []apia2a.HITLQuestion{{Question: question}},
				Nested: &apia2a.NestedHITLRequest{
					TaskID: "grandchild-task", ContextID: "grandchild-context", SubagentName: "grandchild_agent",
					Tools: []apia2a.HITLTool{{
						ID: "confirm-2", CallID: "confirm-2", Name: "ask_user",
						Args: map[string]any{"questions": []map[string]any{{"question": question}}},
					}},
				},
			}),
		},
	}
	state := BuildRemoteHitlState(task, "github_agent")
	if state == nil || state.AskUserRequest == nil || state.AskUserRequest.Nested == nil {
		t.Fatalf("state = %#v", state)
	}
	if _, ok := state.AskUserRequest.Nested.Tools[0].Args["questions"].([]map[string]any); ok {
		t.Fatalf("nested tool args decoded as []map[string]any; test no longer exercises the []any round-trip shape")
	}
	want := "Remote agent 'github_agent' asks: " + question
	if got := RemoteHitlHint(state); got != want {
		t.Fatalf("hint = %q, want %q", got, want)
	}
}

func signInConfirmationMessage() *a2atype.Message {
	return a2atype.NewMessage(a2atype.MessageRoleAgent, confirmationPart("confirm-1", "whoami", "call-1", nil, map[string]any{
		kagentmcp.SignInPayloadKey: map[string]any{
			"server": "gateway", "message": "Sign in to github", "url": "https://gateway.example/oauth/proxy/start?s=1", "elicitation_id": "e1",
		},
	}))
}

func TestBuildHITLStatusMessageSignIn(t *testing.T) {
	message := BuildHITLStatusMessage(signInConfirmationMessage(), true)
	request := GetURLElicitationRequest(message)
	if request == nil {
		t.Fatalf("status message carries no URL elicitation request: %#v", message.Metadata)
	}
	want := apia2a.URLElicitationRequest{
		Type: apia2a.HITLTypeURLElicitationRequest, ID: "confirm-1", Server: "gateway",
		Message: "Sign in to github", URL: "https://gateway.example/oauth/proxy/start?s=1", ElicitationID: "e1",
	}
	if *request != want {
		t.Fatalf("request = %#v, want %#v", *request, want)
	}
	if GetToolApprovalRequest(message) != nil {
		t.Fatal("a sign-in is presented as a tool approval")
	}

	if text := messageText(message); text != "Sign in to github https://gateway.example/oauth/proxy/start?s=1" {
		t.Fatalf("a client that renders only the text gets no link: %q", text)
	}
	if text := messageText(BuildHITLStatusMessage(signInConfirmationMessage(), false)); text != "gateway asks you to open a link to continue." {
		t.Fatalf("a client without the extension may hand the text to a model, so it names the server alone: %q", text)
	}
	if GetURLElicitationRequest(BuildHITLStatusMessage(signInConfirmationMessage(), false)) != nil {
		t.Fatal("a client without the extension receives the typed request")
	}
}

// remoteSignInConfirmationMessage is the parent's pause on a child agent
// that paused on a URL elicitation.
func remoteSignInConfirmationMessage() *a2atype.Message {
	state := RemoteHitlState{
		TaskID: "child-task", ContextID: "child-context", SubagentName: "worker",
		URLElicitationRequest: &apia2a.URLElicitationRequest{
			Type: apia2a.HITLTypeURLElicitationRequest, ID: "child-sign-in", Server: "github",
			Message: "Open https://gateway.example/oauth/proxy/start?s=1", URL: "https://gateway.example/oauth/proxy/start?s=1", ElicitationID: "e1",
		},
	}
	return a2atype.NewMessage(a2atype.MessageRoleAgent, confirmationPart("parent-confirm", "worker", "parent-call", nil, state.ToMap()))
}

func TestBuildHITLStatusMessageRelaysARemoteSignIn(t *testing.T) {
	message := BuildHITLStatusMessage(remoteSignInConfirmationMessage(), true)
	request := GetURLElicitationRequest(message)
	if request == nil {
		t.Fatalf("status message carries no URL elicitation request: %#v", message.Metadata)
	}
	want := apia2a.URLElicitationRequest{
		Type: apia2a.HITLTypeURLElicitationRequest, ID: "parent-confirm", Server: "github",
		Message: "Open https://gateway.example/oauth/proxy/start?s=1", URL: "https://gateway.example/oauth/proxy/start?s=1", ElicitationID: "e1",
		Nested: &apia2a.NestedHITLRequest{
			SubagentName: "worker", TaskID: "child-task", ContextID: "child-context",
			Tools: []apia2a.HITLTool{{ID: "child-sign-in", CallID: "child-sign-in", Name: urlElicitationToolName, Args: map[string]any{}}},
		},
	}
	if !reflect.DeepEqual(*request, want) {
		t.Fatalf("request = %#v, want %#v", *request, want)
	}
	if text := messageText(BuildHITLStatusMessage(remoteSignInConfirmationMessage(), false)); strings.Contains(text, "https://") {
		t.Fatalf("a client without the extension gets the URL in the text: %q", text)
	}
}

func TestBuildResumeHITLMessageNestedSignIn(t *testing.T) {
	stored := &a2atype.Task{Status: a2atype.TaskStatus{
		State:   a2atype.TaskStateInputRequired,
		Message: BuildHITLStatusMessage(remoteSignInConfirmationMessage(), true),
	}}
	for _, action := range []string{apia2a.URLElicitationAccept, apia2a.URLElicitationDecline, apia2a.URLElicitationCancel} {
		incoming := hitlDecisionMessage(&apia2a.URLElicitationResponse{Type: apia2a.HITLTypeURLElicitationResponse, ID: "parent-confirm", Action: action})
		resume, err := BuildResumeHITLMessage(stored, incoming)
		if err != nil || len(resume.Parts) != 1 {
			t.Fatalf("%s: resume = %#v, err = %v", action, resume, err)
		}
		data, _ := resume.Parts[0].Data().(map[string]any)
		if data["id"] != "parent-confirm" {
			t.Fatalf("%s: the parent's confirmation is not answered: %#v", action, data)
		}
		response, _ := data["response"].(map[string]any)
		var confirmation toolconfirmation.ToolConfirmation
		if err := json.Unmarshal([]byte(stringValue(response["response"])), &confirmation); err != nil {
			t.Fatalf("%s: decode confirmation from %#v: %v", action, data, err)
		}
		if !confirmation.Confirmed {
			t.Fatalf("%s: the call to the child is not confirmed, so the child never hears the answer", action)
		}
		state := ParseRemoteHitlState(confirmation.Payload.(map[string]any))
		if state == nil || state.TaskID != "child-task" || state.URLElicitationResponse == nil ||
			state.URLElicitationResponse.ID != "child-sign-in" || state.URLElicitationResponse.Action != action {
			t.Fatalf("%s: nested state = %#v", action, state)
		}
	}

	wrong := hitlDecisionMessage(&apia2a.URLElicitationResponse{Type: apia2a.HITLTypeURLElicitationResponse, ID: "child-sign-in", Action: apia2a.URLElicitationAccept})
	if _, err := BuildResumeHITLMessage(stored, wrong); err == nil {
		t.Fatal("a response to the child's ID is accepted; clients answer the request's own ID")
	}
}

func TestRemoteHitlStateURLElicitationRoundTrip(t *testing.T) {
	task := &a2atype.Task{ID: "child-task", ContextID: "child-context", Status: a2atype.TaskStatus{
		Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent), &apia2a.URLElicitationRequest{
			Type: apia2a.HITLTypeURLElicitationRequest, ID: "child-sign-in", Server: "github", URL: "https://gateway.example/s",
		}),
	}}
	state := BuildRemoteHitlState(task, "worker")
	if state == nil || state.URLElicitationRequest == nil {
		t.Fatalf("state = %#v", state)
	}
	if hint := RemoteHitlHint(state); hint != "Remote agent 'worker' needs the person to sign in to github." {
		t.Fatalf("hint = %q", hint)
	}
	state.URLElicitationResponse = &apia2a.URLElicitationResponse{Type: apia2a.HITLTypeURLElicitationResponse, ID: "child-sign-in", Action: apia2a.URLElicitationDecline}
	parsed := ParseRemoteHitlState(state.ToMap())
	if parsed == nil || !reflect.DeepEqual(parsed.URLElicitationRequest, state.URLElicitationRequest) ||
		!reflect.DeepEqual(parsed.URLElicitationResponse, state.URLElicitationResponse) {
		t.Fatalf("round trip = %#v, want %#v", parsed, state)
	}
	if parsed.ResponseType() != apia2a.HITLTypeURLElicitationResponse || parsed.ResponsePayload() != parsed.URLElicitationResponse {
		t.Fatalf("response = %s %#v", parsed.ResponseType(), parsed.ResponsePayload())
	}
}

func TestRemoteHitlStateRefusesAChildURLThatIsNotSafeToShow(t *testing.T) {
	task := &a2atype.Task{ID: "child-task", ContextID: "child-context", Status: a2atype.TaskStatus{
		Message: AttachHitlExtension(a2atype.NewMessage(a2atype.MessageRoleAgent), &apia2a.URLElicitationRequest{
			Type: apia2a.HITLTypeURLElicitationRequest, ID: "child-sign-in", Server: "github", URL: "javascript:alert(1)",
		}),
	}}
	if state := BuildRemoteHitlState(task, "worker"); state != nil {
		t.Fatalf("a child's javascript: URL is relayed: %#v", state)
	}
	stored := RemoteHitlState{TaskID: "child-task", SubagentName: "worker", URLElicitationRequest: &apia2a.URLElicitationRequest{
		Type: apia2a.HITLTypeURLElicitationRequest, ID: "child-sign-in", Server: "github", URL: "http://gateway.example/s",
	}}
	if state := ParseRemoteHitlState(stored.ToMap()); state != nil {
		t.Fatalf("a stored request with a plain http URL is accepted: %#v", state)
	}
}

func TestBuildResumeHITLMessageSignIn(t *testing.T) {
	stored := &a2atype.Task{Status: a2atype.TaskStatus{
		State:   a2atype.TaskStateInputRequired,
		Message: BuildHITLStatusMessage(signInConfirmationMessage(), true),
	}}
	for _, test := range []struct {
		action    string
		confirmed bool
	}{
		{apia2a.URLElicitationAccept, true},
		{apia2a.URLElicitationDecline, false},
		{apia2a.URLElicitationCancel, false},
	} {
		incoming := hitlDecisionMessage(&apia2a.URLElicitationResponse{Type: apia2a.HITLTypeURLElicitationResponse, ID: "confirm-1", Action: test.action})
		if !IsHITLResponse(incoming) {
			t.Fatalf("%s: a URL elicitation response is not a HITL response", test.action)
		}
		resume, err := BuildResumeHITLMessage(stored, incoming)
		if err != nil || len(resume.Parts) != 1 {
			t.Fatalf("%s: resume = %#v, err = %v", test.action, resume, err)
		}
		data, _ := resume.Parts[0].Data().(map[string]any)
		response, _ := data["response"].(map[string]any)
		var confirmation toolconfirmation.ToolConfirmation
		if err := json.Unmarshal([]byte(stringValue(response["response"])), &confirmation); err != nil {
			t.Fatalf("%s: decode confirmation from %#v: %v", test.action, data, err)
		}
		if confirmation.Confirmed != test.confirmed {
			t.Fatalf("%s: confirmed = %v, want %v", test.action, confirmation.Confirmed, test.confirmed)
		}
	}

	other := hitlDecisionMessage(&apia2a.URLElicitationResponse{Type: apia2a.HITLTypeURLElicitationResponse, ID: "confirm-2", Action: apia2a.URLElicitationAccept})
	if _, err := BuildResumeHITLMessage(stored, other); err == nil {
		t.Fatal("BuildResumeHITLMessage() accepted a response to another sign-in")
	}
}
