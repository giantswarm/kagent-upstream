package kagent

import (
	"slices"
	"testing"

	a2atype "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/stretchr/testify/require"
	"istio.io/istio/pkg/kube/krt"

	apia2a "github.com/kagent-dev/kagent/go/api/a2a"
	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
	"github.com/kagent-dev/kagent/go/core/pkg/env"
)

func TestRequestMetadataDiscoveryRequiresKnownGoRuntime(t *testing.T) {
	const image = "example.com/go-adk@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, test := range []struct {
		name       string
		knownImage string
		workload   v1alpha3.HarnessWorkload
		want       bool
	}{
		{name: "released Go runtime", knownImage: image, workload: v1alpha3.HarnessWorkload{Image: image}, want: true},
		{name: "unknown image", knownImage: image, workload: v1alpha3.HarnessWorkload{Image: "example.com/custom@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		{name: "unknown release", workload: v1alpha3.HarnessWorkload{Image: image}},
		{name: "entrypoint override", knownImage: image, workload: v1alpha3.HarnessWorkload{Image: image, Command: []string{"kagent-adk"}}},
		{name: "arguments override", knownImage: image, workload: v1alpha3.HarnessWorkload{Image: image, Args: []string{"static"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(env.GoADKReleaseImage.Name(), test.knownImage)
			compiler := NewCompiler(krt.TestingDummyContext{}, translator.Collections{})
			card := compiler.agentCard("metadata-agent", &translator.TemplateConfiguration{}, test.workload)
			found := slices.ContainsFunc(card.Capabilities.Extensions, func(extension a2atype.AgentExtension) bool {
				return extension.URI == apia2a.MCPRequestMetadataExtensionURI
			})
			require.Equal(t, test.want, found)
			require.Contains(t, card.Capabilities.Extensions, apia2a.HITLExtension())
		})
	}
}
