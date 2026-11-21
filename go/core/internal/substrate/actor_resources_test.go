package substrate

import (
	"testing"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

const (
	resourceTestAtespace = "test"
	resourceTestMemory   = "1Gi"
)

func TestActorResources(t *testing.T) {
	for _, scenario := range []struct {
		name, cpu, memory, wantError string
	}{
		{name: "fractional CPU", cpu: "500m", memory: "2Gi"},
		{name: "whole CPU", cpu: "2", memory: "512Mi"},
		{name: "zero CPU", cpu: "0", memory: resourceTestMemory, wantError: runtimeCPUResource},
		{name: "negative CPU", cpu: "-1", memory: resourceTestMemory, wantError: runtimeCPUResource},
		{name: "invalid CPU", cpu: "cores", memory: resourceTestMemory, wantError: runtimeCPUResource},
		{name: "CPU upper bound", cpu: "1000", memory: resourceTestMemory, wantError: runtimeCPUResource},
		{name: "zero memory", cpu: "1", memory: "0", wantError: runtimeMemoryResource},
		{name: "negative memory", cpu: "1", memory: "-1Gi", wantError: runtimeMemoryResource},
		{name: "invalid memory", cpu: "1", memory: "bytes", wantError: runtimeMemoryResource},
		{name: "memory overflow", cpu: "1", memory: "10E", wantError: runtimeMemoryResource},
		{name: "CPU missing", memory: resourceTestMemory, wantError: runtimeCPUResource},
		{name: "memory missing", cpu: "1", wantError: runtimeMemoryResource},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			resources, err := actorResources(scenario.cpu, scenario.memory)
			if scenario.wantError != "" {
				require.ErrorContains(t, err, "invalid runtime "+scenario.wantError+" limit")
				require.Nil(t, resources)
				return
			}
			require.NoError(t, err)
			require.True(t, proto.Equal(&ateapipb.Resources{Limits: []*ateapipb.Limits{
				{Name: runtimeCPUResource, Quantity: scenario.cpu}, {Name: runtimeMemoryResource, Quantity: scenario.memory},
			}}, resources))
		})
	}
}

func TestActorResourcesPreservesUnsizedRevision(t *testing.T) {
	resources, err := actorResources("", "")
	require.NoError(t, err)
	require.Nil(t, resources)
}

func TestSessionToolTemplatePreservesResourceLimits(t *testing.T) {
	limits, err := actorResources("1500m", "1536Mi")
	require.NoError(t, err)
	base := &ateapipb.ActorTemplate{
		Metadata:   &ateapipb.ResourceMetadata{Atespace: resourceTestAtespace, Name: "base"},
		Resources:  limits,
		Containers: []*ateapipb.Container{{Name: "kagent"}},
	}
	before := proto.CloneOf(base)
	derived, err := ActorTemplateWithTools(base, "helper", translator.RevisionID{1}, []translator.ToolImage{
		{Name: "node", Reference: "example.com/node@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	})
	require.NoError(t, err)
	require.True(t, proto.Equal(limits, derived.Resources))
	derived.Resources.Limits[0].Quantity = "2"
	require.True(t, proto.Equal(before, base))
	require.False(t, ActorTemplateSpecEqual(base, derived))
}
