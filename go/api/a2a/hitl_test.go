package a2a

import (
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
)

func TestParseToolApprovalResponseAcceptsEveryPendingDecision(t *testing.T) {
	message := &a2atype.Message{}
	err := AttachHITL(message, ToolApprovalResponse{
		Type: HITLTypeToolApprovalResponse,
		Approvals: []ToolApproval{
			{ID: "one", Approved: true},
			{ID: "two", Approved: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	response, err := ParseToolApprovalResponse(message)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Approvals) != 2 {
		t.Fatalf("got %d decisions, want 2", len(response.Approvals))
	}
}

func TestValidateToolApprovalResponseRejectsReasonOnApproval(t *testing.T) {
	request := &ToolApprovalRequest{Tools: []HITLTool{{ID: "one"}}}
	response := &ToolApprovalResponse{Approvals: []ToolApproval{{
		ID: "one", Approved: true, RejectionReason: "contradictory",
	}}}

	if err := ValidateToolApprovalResponse(request, response); err == nil {
		t.Fatal("ValidateToolApprovalResponse() accepted a rejection reason on approval")
	}
}

func TestValidateToolApprovalResponseUsesNestedToolIDs(t *testing.T) {
	request := &ToolApprovalRequest{
		Tools:  []HITLTool{{ID: "parent"}},
		Nested: &NestedHITLRequest{Tools: []HITLTool{{ID: "child-one"}, {ID: "child-two"}}},
	}
	response := &ToolApprovalResponse{Approvals: []ToolApproval{
		{ID: "child-one", Approved: true},
		{ID: "child-two", Approved: false},
	}}

	if err := ValidateToolApprovalResponse(request, response); err != nil {
		t.Fatalf("ValidateToolApprovalResponse() rejected nested decisions: %v", err)
	}
}

func TestParseAndValidateAskUserResponse(t *testing.T) {
	requestMessage := &a2atype.Message{}
	if err := AttachHITL(requestMessage, AskUserRequest{
		Type: HITLTypeAskUserRequest,
		ID:   "question-1",
		Questions: []HITLQuestion{
			{Question: "Which namespace?"},
			{Question: "Which cluster?"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	request, err := ParseAskUserRequest(requestMessage)
	if err != nil {
		t.Fatal(err)
	}

	responseMessage := &a2atype.Message{}
	if err := AttachHITL(responseMessage, AskUserResponse{
		Type: HITLTypeAskUserResponse,
		ID:   "question-1",
		Answers: []AskUserAnswer{
			{Answer: []string{"default"}},
			{Answer: []string{"production"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	response, err := ParseAskUserResponse(responseMessage)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAskUserResponse(request, response); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAskUserResponseRequiresEveryAnswer(t *testing.T) {
	request := &AskUserRequest{ID: "question-1", Questions: []HITLQuestion{{Question: "One?"}, {Question: "Two?"}}}
	response := &AskUserResponse{ID: "question-1", Answers: []AskUserAnswer{{Answer: []string{"one"}}}}
	if err := ValidateAskUserResponse(request, response); err == nil {
		t.Fatal("ValidateAskUserResponse() accepted an incomplete response")
	}
}

func TestParseAndValidateURLElicitation(t *testing.T) {
	requestMessage := &a2atype.Message{}
	if err := AttachHITL(requestMessage, URLElicitationRequest{
		Type: HITLTypeURLElicitationRequest, ID: "sign-in", Server: "github",
		Message: "Sign in to github", URL: "https://gateway.example/oauth/proxy/start?s=1", ElicitationID: "e1",
	}); err != nil {
		t.Fatal(err)
	}
	request, err := ParseURLElicitationRequest(requestMessage)
	if err != nil || request == nil {
		t.Fatalf("ParseURLElicitationRequest() = %v, %v", request, err)
	}
	if request.Server != "github" || request.ElicitationID != "e1" {
		t.Fatalf("request = %+v", request)
	}
	if other, err := ParseToolApprovalRequest(requestMessage); other != nil || err != nil {
		t.Fatalf("ParseToolApprovalRequest() = %v, %v, want no request", other, err)
	}

	for _, action := range []string{URLElicitationAccept, URLElicitationDecline, URLElicitationCancel} {
		responseMessage := &a2atype.Message{}
		if err := AttachHITL(responseMessage, URLElicitationResponse{
			Type: HITLTypeURLElicitationResponse, ID: "sign-in", Action: action,
		}); err != nil {
			t.Fatal(err)
		}
		response, err := ParseURLElicitationResponse(responseMessage)
		if err != nil {
			t.Fatalf("ParseURLElicitationResponse(%s): %v", action, err)
		}
		if err := ValidateURLElicitationResponse(request, response); err != nil {
			t.Fatalf("ValidateURLElicitationResponse(%s): %v", action, err)
		}
	}
}

func TestURLElicitationRejectsMalformedPayloads(t *testing.T) {
	requestMessage := &a2atype.Message{}
	if err := AttachHITL(requestMessage, URLElicitationRequest{Type: HITLTypeURLElicitationRequest, ID: "sign-in"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseURLElicitationRequest(requestMessage); err == nil {
		t.Fatal("ParseURLElicitationRequest() accepted a request without a URL")
	}

	responseMessage := &a2atype.Message{}
	if err := AttachHITL(responseMessage, URLElicitationResponse{
		Type: HITLTypeURLElicitationResponse, ID: "sign-in", Action: "approve",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseURLElicitationResponse(responseMessage); err == nil {
		t.Fatal("ParseURLElicitationResponse() accepted an unknown action")
	}

	request := &URLElicitationRequest{ID: "sign-in"}
	if err := ValidateURLElicitationResponse(request, &URLElicitationResponse{ID: "other", Action: URLElicitationAccept}); err == nil {
		t.Fatal("ValidateURLElicitationResponse() accepted a mismatched ID")
	}
}
