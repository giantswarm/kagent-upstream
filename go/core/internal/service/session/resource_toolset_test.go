package session

import (
	"testing"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestToolsetRepointChecksPinnedResourcesBeforePreparingTemplate(t *testing.T) {
	const initialMemory = "1Gi"
	const increasedMemory = "2Gi"
	limits := func(cpu, memory string) *ateapipb.Resources {
		return &ateapipb.Resources{Limits: []*ateapipb.Limits{{Name: "cpu", Quantity: cpu}, {Name: "memory", Quantity: memory}}}
	}
	initial := limits("1", initialMemory)
	for _, scenario := range []struct {
		name                          string
		pinned, current               *ateapipb.Resources
		missingPinned, replacedPinned bool
		wantPrepared                  bool
	}{
		{name: "same limits", pinned: initial, current: proto.CloneOf(initial), wantPrepared: true},
		{name: "both unsized", wantPrepared: true},
		{name: "CPU change", pinned: initial, current: limits("2", initialMemory)},
		{name: "memory change", pinned: initial, current: limits("1", increasedMemory)},
		{name: "introducing limits", current: initial},
		{name: "removing limits", pinned: initial},
		{name: "pinned template missing", pinned: initial, current: initial, missingPinned: true},
		{name: "pinned template recreated", pinned: initial, current: initial, replacedPinned: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			current, templates := toolsetTestBase()
			templates.templates[current.ActorTemplateName].Resources = scenario.current
			pinned := *current
			pinned.Revision = "sizing-pinned-revision"
			pinned.ActorTemplateName = "sizing-pinned-template"
			pinned.ActorTemplateUID = "sizing-pinned-uid"
			pinnedTemplate := proto.CloneOf(templates.templates[current.ActorTemplateName])
			pinnedTemplate.Metadata = &ateapipb.ResourceMetadata{Atespace: pinned.ActorTemplateAtespace, Name: pinned.ActorTemplateName, Uid: pinned.ActorTemplateUID}
			pinnedTemplate.Resources = scenario.pinned
			if scenario.replacedPinned {
				pinnedTemplate.Metadata.Uid = "replacement-uid"
			}
			if !scenario.missingPinned {
				templates.templates[pinned.ActorTemplateName] = pinnedTemplate
			}
			workflow := &ActorWorkflow{toolImages: toolsetTestService(t, &serviceTestStore{}, templates).toolImages}
			session := &apiv1alpha1.Session{PreparedRevision: pinned.Revision, ToolImages: toolsetTestImages()}
			for attempt := range 2 {
				target := workflow.toolsetRepointTarget(t.Context(), session, &pinned, current)
				if !scenario.wantPrepared {
					require.Nil(t, target)
				}
				if scenario.wantPrepared && attempt == 1 {
					require.NotNil(t, target)
				}
			}
			require.Equal(t, pinned.Revision, session.PreparedRevision)
			if scenario.wantPrepared {
				require.Len(t, templates.created, 1)
			} else {
				require.Empty(t, templates.created)
			}
		})
	}
}
