package models

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// anthropicTestServer serves a canned Messages API response to a model built
// from cfg and records the request body the SDK sent.
func anthropicTestServer(t *testing.T, cfg *AnthropicConfig, handler func(w http.ResponseWriter)) (*AnthropicModel, *[]byte) {
	t.Helper()
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		captured = body
		handler(w)
	}))
	t.Cleanup(server.Close)

	cfg.BaseUrl = server.URL
	m, err := newAnthropicModelFromConfig(context.Background(), cfg, "test-key")
	require.NoError(t, err)
	return m, &captured
}

// anthropicEventStream serves Messages API stream events the way the API does:
// one server-sent event per stream event, named after its type.
func anthropicEventStream(t *testing.T, events []string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range events {
			var envelope struct {
				Type string `json:"type"`
			}
			require.NoError(t, json.Unmarshal([]byte(event), &envelope))
			_, _ = io.WriteString(w, "event: "+envelope.Type+"\ndata: "+event+"\n\n")
		}
	}
}

func anthropicTestRequest() *model.LLMRequest {
	return &model.LLMRequest{
		Model:    "anthropic",
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "list the pods"}}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{
				{Name: "list_pods", Description: "list pods"},
			}}},
		},
	}
}

// A ModelConfig's maxTokens is sized for the agent's streamed replies, and the
// SDK refuses a non-streaming call whose max_tokens is above 21,333 before
// sending it. A non-streaming caller, the context compaction summarizer among
// them, still gets its one final response: the call streams and the message is
// accumulated.
func TestAnthropicNonStreamingAnswersAboveTheSDKsNonStreamingMaxTokens(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":12,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pod-a is "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"running."}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"list_pods","input":{}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"namespace\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"kagent\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":9}}`,
		`{"type":"message_stop"}`,
	}
	maxTokens := 32000
	m, captured := anthropicTestServer(t, &AnthropicConfig{Model: "claude-sonnet-4-6", MaxTokens: &maxTokens}, anthropicEventStream(t, events))

	var responses []*model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), anthropicTestRequest(), false) {
		require.NoError(t, err)
		responses = append(responses, resp)
	}

	var wire struct {
		Stream    bool `json:"stream"`
		MaxTokens int  `json:"max_tokens"`
	}
	require.NoError(t, json.Unmarshal(*captured, &wire))
	assert.True(t, wire.Stream)
	assert.Equal(t, 32000, wire.MaxTokens)

	require.Len(t, responses, 1, "a non-streaming call yields its final response only")
	final := responses[0]
	assert.False(t, final.Partial)
	assert.True(t, final.TurnComplete)
	assert.Equal(t, genai.FinishReasonStop, final.FinishReason)
	assert.Equal(t, &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 12, CandidatesTokenCount: 9}, final.UsageMetadata)
	require.Len(t, final.Content.Parts, 2)
	assert.Equal(t, "pod-a is running.", final.Content.Parts[0].Text)
	call := final.Content.Parts[1].FunctionCall
	require.NotNil(t, call)
	assert.Equal(t, "toolu_1", call.ID)
	assert.Equal(t, "list_pods", call.Name)
	assert.Equal(t, map[string]any{"namespace": "kagent"}, call.Args)
}

func TestAnthropicNonStreamingReportsAnAPIError(t *testing.T) {
	m, _ := anthropicTestServer(t, &AnthropicConfig{Model: "claude-sonnet-4-6"}, func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long"}}`)
	})

	var errs []error
	for resp, err := range m.GenerateContent(context.Background(), anthropicTestRequest(), false) {
		assert.Nil(t, resp)
		errs = append(errs, err)
	}

	require.Len(t, errs, 1)
	require.Error(t, errs[0])
	assert.Contains(t, errs[0].Error(), "anthropic API error")
	assert.Contains(t, errs[0].Error(), "prompt is too long")
}
