package translator

import (
	"testing"

	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	runtimeTestDefaultMemory = "768Mi"
)

const (
	runtimeTestDefaultCPU     = "750m"
	runtimeTestOverrideMemory = "2Gi"
)

func TestHarnessLimitsUsesConfiguredDefaultsIndependently(t *testing.T) {
	defaults := RuntimeDefaults{CPU: runtimeTestDefaultCPU, Memory: runtimeTestDefaultMemory}
	for _, scenario := range []struct {
		name        string
		resources   *v1alpha3.HarnessResources
		cpu, memory string
	}{
		{name: "defaults", cpu: runtimeTestDefaultCPU, memory: runtimeTestDefaultMemory},
		{name: "CPU", resources: &v1alpha3.HarnessResources{CPU: new(resource.MustParse("2"))}, cpu: "2", memory: runtimeTestDefaultMemory},
		{name: "memory", resources: &v1alpha3.HarnessResources{Memory: new(resource.MustParse(runtimeTestOverrideMemory))}, cpu: runtimeTestDefaultCPU, memory: runtimeTestOverrideMemory},
		{name: "both", resources: &v1alpha3.HarnessResources{CPU: new(resource.MustParse("2")), Memory: new(resource.MustParse(runtimeTestOverrideMemory))}, cpu: "2", memory: runtimeTestOverrideMemory},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			cpu, memory := harnessLimits(&HarnessConfiguration{Spec: v1alpha3.HarnessSpec{Resources: scenario.resources}}, defaults)
			require.Equal(t, scenario.cpu, cpu)
			require.Equal(t, scenario.memory, memory)
		})
	}
}

func TestRuntimeLimitsChangeRevisionIdentity(t *testing.T) {
	revision := Revision{CPU: "1", Memory: "1Gi"}
	initial, err := revision.Digest()
	require.NoError(t, err)
	for _, sized := range []Revision{{CPU: "2", Memory: "1Gi"}, {CPU: "1", Memory: runtimeTestOverrideMemory}} {
		changed, err := sized.Digest()
		require.NoError(t, err)
		require.NotEqual(t, initial, changed)
	}
}
