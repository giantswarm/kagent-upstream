package substrate

import (
	"context"
	"testing"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	pinnedResourceTemplate = "pinned"
	targetResourceTemplate = "target"
)

type resourcePinningControl struct {
	ateapipb.ControlClient
	actor     *ateapipb.Actor
	templates map[string]*ateapipb.ActorTemplate
	getError  error
	updates   int
}

func (c *resourcePinningControl) GetActor(context.Context, *ateapipb.GetActorRequest, ...grpc.CallOption) (*ateapipb.Actor, error) {
	return proto.CloneOf(c.actor), nil
}

func (c *resourcePinningControl) GetActorTemplate(_ context.Context, request *ateapipb.GetActorTemplateRequest, _ ...grpc.CallOption) (*ateapipb.ActorTemplate, error) {
	if c.getError != nil {
		return nil, c.getError
	}
	template := c.templates[request.ActorTemplate.Name]
	if template == nil {
		return nil, status.Error(codes.NotFound, "template missing")
	}
	return template, nil
}

func (c *resourcePinningControl) UpdateActor(_ context.Context, request *ateapipb.UpdateActorRequest, _ ...grpc.CallOption) (*ateapipb.Actor, error) {
	c.updates++
	c.actor = request.Actor
	return proto.CloneOf(c.actor), nil
}

func TestRepointActorPreservesResourceLimits(t *testing.T) {
	initial, err := actorResources("1", resourceTestMemory)
	require.NoError(t, err)
	moreCPU, err := actorResources("2", resourceTestMemory)
	require.NoError(t, err)
	moreMemory, err := actorResources("1", "2Gi")
	require.NoError(t, err)
	for _, scenario := range []struct {
		name             string
		previous, target *ateapipb.Resources
		wantCode         codes.Code
	}{
		{name: "same limits", previous: initial, target: proto.CloneOf(initial)},
		{name: "unsized revisions"},
		{name: "CPU change", previous: initial, target: moreCPU, wantCode: codes.FailedPrecondition},
		{name: "memory change", previous: initial, target: moreMemory, wantCode: codes.FailedPrecondition},
		{name: "introducing limits", target: initial, wantCode: codes.FailedPrecondition},
		{name: "removing limits", previous: initial, wantCode: codes.FailedPrecondition},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			control := &resourcePinningControl{
				actor: &ateapipb.Actor{ActorTemplate: &ateapipb.ObjectRef{Atespace: resourceTestAtespace, Name: pinnedResourceTemplate}},
				templates: map[string]*ateapipb.ActorTemplate{
					pinnedResourceTemplate: {Resources: scenario.previous}, targetResourceTemplate: {Resources: scenario.target},
				},
			}
			client := &Client{ControlClient: control, cfg: Config{CallTimeout: time.Second}}
			actor, err := client.RepointActor(t.Context(), resourceTestAtespace, "session", resourceTestAtespace, targetResourceTemplate)
			require.Equal(t, scenario.wantCode, status.Code(err))
			if scenario.wantCode != codes.OK {
				require.Nil(t, actor)
				require.Zero(t, control.updates)
				require.Equal(t, pinnedResourceTemplate, control.actor.ActorTemplate.Name)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, control.updates)
			require.Equal(t, targetResourceTemplate, actor.ActorTemplate.Name)
		})
	}
}

func TestRepointActorDoesNotMoveWhenTemplateCannotBeRead(t *testing.T) {
	for _, missing := range []string{pinnedResourceTemplate, targetResourceTemplate} {
		t.Run(missing, func(t *testing.T) {
			control := &resourcePinningControl{
				actor:     &ateapipb.Actor{ActorTemplate: &ateapipb.ObjectRef{Atespace: resourceTestAtespace, Name: pinnedResourceTemplate}},
				templates: map[string]*ateapipb.ActorTemplate{pinnedResourceTemplate: {}, targetResourceTemplate: {}},
			}
			delete(control.templates, missing)
			client := &Client{ControlClient: control, cfg: Config{CallTimeout: time.Second}}
			_, err := client.RepointActor(t.Context(), resourceTestAtespace, "session", resourceTestAtespace, targetResourceTemplate)
			require.Equal(t, codes.NotFound, status.Code(err))
			require.Zero(t, control.updates)
		})
	}
}

func TestRepointActorAlreadyPinnedNeedsNoTemplateRead(t *testing.T) {
	control := &resourcePinningControl{
		actor:    &ateapipb.Actor{ActorTemplate: &ateapipb.ObjectRef{Atespace: resourceTestAtespace, Name: pinnedResourceTemplate}},
		getError: status.Error(codes.Unavailable, "templates unavailable"),
	}
	client := &Client{ControlClient: control, cfg: Config{CallTimeout: time.Second}}
	actor, err := client.RepointActor(t.Context(), resourceTestAtespace, "session", resourceTestAtespace, pinnedResourceTemplate)
	require.NoError(t, err)
	require.Equal(t, pinnedResourceTemplate, actor.ActorTemplate.Name)
	require.Zero(t, control.updates)
}
