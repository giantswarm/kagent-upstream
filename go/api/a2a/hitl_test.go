package a2a

import (
	"strings"
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

func TestValidateElicitationURL(t *testing.T) {
	for _, test := range []struct {
		url   string
		valid bool
	}{
		{"https://gateway.example/oauth/proxy/start?s=1", true},
		{"http://localhost:8080/callback", true},
		{"http://127.0.0.1/callback", true},
		{"http://[::1]/callback", true},
		{"http://gateway.example/s", false},
		{"javascript:alert(1)", false},
		{"data:text/html,hi", false},
		{"https://github.com@evil.example/s", false},
		{"https:///no-host", false},
		{"", false},
	} {
		err := ValidateElicitationURL(test.url)
		if (err == nil) != test.valid {
			t.Errorf("ValidateElicitationURL(%q) = %v, want valid %v", test.url, err, test.valid)
		}
	}

	message := &a2atype.Message{}
	if err := AttachHITL(message, URLElicitationRequest{Type: HITLTypeURLElicitationRequest, ID: "sign-in", URL: "javascript:alert(1)"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseURLElicitationRequest(message); err == nil {
		t.Fatal("ParseURLElicitationRequest() accepted a javascript: URL")
	}
}

func TestURLElicitationText(t *testing.T) {
	request := URLElicitationRequest{Server: "github", URL: "https://gateway.example/s"}
	if text := URLElicitationText(request, true); text != "github asks you to open a link to continue. https://gateway.example/s" {
		t.Fatalf("activated text = %q", text)
	}
	if text := URLElicitationText(request, false); text != "github asks you to open a link to continue." {
		t.Fatalf("text for a client without the extension = %q", text)
	}
}

func TestURLElicitationTextLeavesOutTheServersMessage(t *testing.T) {
	for _, message := range []string{
		"Open https://evil.example/phish to continue",
		"Open [the sign-in page](https://evil.example/phish)",
		"Open www.evil.example/phish",
	} {
		request := URLElicitationRequest{Server: "github", Message: message, URL: "https://gateway.example/s"}
		for _, activated := range []bool{true, false} {
			text := URLElicitationText(request, activated)
			if strings.Contains(text, "evil.example") {
				t.Errorf("URLElicitationText(message %q, activated %v) = %q, carries the message's link", message, activated, text)
			}
		}
	}
}

func TestURLElicitationTextWarnsOfAnInternationalizedHost(t *testing.T) {
	for _, test := range []struct {
		url  string
		host string
	}{
		{"https://xn--gthub-n4a.com/login", "xn--gthub-n4a.com"},
		{"https://g\u0456thub.com/login", "xn--gthub-n2e.com"},
		{"https://login.XN--gthub-n4a.com/", "login.xn--gthub-n4a.com"},
	} {
		if err := ValidateElicitationURL(test.url); err != nil {
			t.Errorf("ValidateElicitationURL(%q) = %v, want accepted", test.url, err)
		}
		text := URLElicitationText(URLElicitationRequest{Server: "github", URL: test.url}, true)
		want := "github asks you to open a link to continue. " + test.url +
			" Check the link's host before opening it: " + test.host + " is an internationalized domain name, which can look like another site's."
		if text != want {
			t.Errorf("URLElicitationText(%q) = %q, want %q", test.url, text, want)
		}
		if text := URLElicitationText(URLElicitationRequest{Server: "github", URL: test.url}, false); strings.Contains(text, "xn--") {
			t.Errorf("text for a client without the extension names the host: %q", text)
		}
	}
	if text := URLElicitationText(URLElicitationRequest{Server: "github", URL: "https://github.com/login"}, true); strings.Contains(text, "Check") {
		t.Errorf("text for an ASCII host warns: %q", text)
	}
}
