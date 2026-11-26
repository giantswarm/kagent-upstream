package a2a

import (
	"iter"
	"slices"
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
)

const agentCardTestName = "agent"

func TestEnsureHITLExtension(t *testing.T) {
	t.Run("attaches when absent", func(t *testing.T) {
		card := &a2atype.AgentCard{Name: agentCardTestName}
		EnsureHITLExtension(card)
		if !slices.ContainsFunc(card.Capabilities.Extensions, func(extension a2atype.AgentExtension) bool { return extension.URI == HITLExtensionURI }) {
			t.Fatalf("extensions = %#v, want the HITL extension", card.Capabilities.Extensions)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		card := &a2atype.AgentCard{Name: agentCardTestName}
		EnsureHITLExtension(card)
		EnsureHITLExtension(card)
		if len(card.Capabilities.Extensions) != 1 {
			t.Errorf("len(extensions) = %d, want 1", len(card.Capabilities.Extensions))
		}
	})

	t.Run("preserves unrelated extensions", func(t *testing.T) {
		card := &a2atype.AgentCard{
			Name: agentCardTestName,
			Capabilities: a2atype.AgentCapabilities{
				Extensions: []a2atype.AgentExtension{{URI: "https://example.com/extensions/other/v1"}},
			},
		}
		EnsureHITLExtension(card)
		if len(card.Capabilities.Extensions) != 2 {
			t.Fatalf("len(extensions) = %d, want 2", len(card.Capabilities.Extensions))
		}
		if card.Capabilities.Extensions[0].URI != "https://example.com/extensions/other/v1" {
			t.Errorf("extensions[0].URI = %q, want the pre-existing extension", card.Capabilities.Extensions[0].URI)
		}
	})

	t.Run("nil card", func(t *testing.T) {
		EnsureHITLExtension(nil)
	})
}

func TestEnrichAgentCardDeclaresHITLExtension(t *testing.T) {
	agent, err := adkagent.New(adkagent.Config{
		Name: "adk_agent",
		Run: func(adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {}
		},
	})
	if err != nil {
		t.Fatalf("adkagent.New: %v", err)
	}
	card := &a2atype.AgentCard{Name: agentCardTestName}

	EnrichAgentCard(card, agent)

	if !slices.ContainsFunc(card.Capabilities.Extensions, func(extension a2atype.AgentExtension) bool { return extension.URI == HITLExtensionURI }) {
		t.Fatalf("extensions = %#v, want the HITL extension", card.Capabilities.Extensions)
	}
}
