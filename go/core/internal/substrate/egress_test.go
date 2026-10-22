package substrate

import (
	"context"
	"testing"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestEnsureActorEgressPolicyRetriesLostResponse(t *testing.T) {
	fake := &egressPolicyFake{createErr: context.DeadlineExceeded}
	client := &Client{ControlClient: fake}
	policy := &ateapipb.EgressPolicy{
		Metadata: &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "default"},
		Rules: []*ateapipb.EgressRule{
			{Http: &ateapipb.HTTPRule{Hostnames: []string{"api.example.com"}}},
			{Https: &ateapipb.HTTPSRule{Hostnames: []string{"api.example.com"}}},
		},
	}
	require.ErrorIs(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", policy), context.DeadlineExceeded)
	require.NotNil(t, fake.policy, "the server committed before the response was lost")
	fake.createErr = nil
	require.NoError(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", policy))
	require.NoError(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", policy))
	require.Equal(t, 1, fake.created)
	require.Zero(t, fake.updated, "a retry finds the policy it wrote")
	require.Equal(t, actorRef("team-a", "actor"), fake.actor)
	fake.getErr = status.Error(codes.Unavailable, "unavailable")
	require.Equal(t, codes.Unavailable, status.Code(client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", &ateapipb.EgressPolicy{})))
}

// A new Actor starts with its template's default allowlist, and a retried
// creation may find the one an earlier attempt wrote from older grants: the
// creation's policy replaces either.
func TestEnsureActorEgressPolicyReplacesTheExistingPolicy(t *testing.T) {
	inherited := &ateapipb.EgressPolicy{Metadata: &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "default", Version: 3}, Rules: httpsRules("git.example.com")}
	policy := &ateapipb.EgressPolicy{Metadata: &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "default"}, Rules: httpsRules("api.example.com")}
	fake := &egressPolicyFake{policy: proto.CloneOf(inherited)}
	client := &Client{ControlClient: fake}

	require.NoError(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", policy))
	require.Equal(t, 1, fake.updated)
	require.True(t, SameRules(policy, fake.policy), "the Actor holds the creation's allowlist")
	require.NoError(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", policy))
	require.Equal(t, 1, fake.updated, "a retry finds the creation's allowlist")
}

func TestApplyActorEgressPolicy(t *testing.T) {
	t.Run("an Actor without a policy has none to change", func(t *testing.T) {
		fake := &egressPolicyFake{getErr: status.Error(codes.NotFound, "no actor")}
		client := &Client{ControlClient: fake}
		require.NoError(t, client.ApplyActorEgressPolicy(t.Context(), "team-a", "actor", func(*ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error) {
			t.Fatal("nothing to compute for a missing policy")
			return nil, nil
		}))
		require.Zero(t, fake.updated)
	})
	t.Run("the same rules write nothing", func(t *testing.T) {
		fake := &egressPolicyFake{policy: &ateapipb.EgressPolicy{Metadata: &ateapipb.ResourceMetadata{Version: 1}, Rules: httpsRules("api.example.com")}}
		client := &Client{ControlClient: fake}
		require.NoError(t, client.ApplyActorEgressPolicy(t.Context(), "team-a", "actor", func(*ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error) {
			return &ateapipb.EgressPolicy{Rules: httpsRules("api.example.com")}, nil
		}))
		require.Zero(t, fake.updated)
	})
	t.Run("a write that lands meanwhile is read again", func(t *testing.T) {
		fake := &egressPolicyFake{policy: &ateapipb.EgressPolicy{Metadata: &ateapipb.ResourceMetadata{Version: 1}, Rules: httpsRules("old.example.com")}}
		client := &Client{ControlClient: fake}
		var seen []string
		require.NoError(t, client.ApplyActorEgressPolicy(t.Context(), "team-a", "actor", func(existing *ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error) {
			seen = append(seen, existing.GetRules()[0].GetHttps().GetHostnames()[0])
			if len(seen) == 1 {
				// A turn opens between the read and the update.
				fake.policy = &ateapipb.EgressPolicy{Metadata: &ateapipb.ResourceMetadata{Version: 2}, Rules: httpsRules("turn.example.com")}
			}
			return &ateapipb.EgressPolicy{Rules: httpsRules("new.example.com")}, nil
		}))
		require.Equal(t, []string{"old.example.com", "turn.example.com"}, seen)
		require.Equal(t, 1, fake.updated)
		require.True(t, SameRules(&ateapipb.EgressPolicy{Rules: httpsRules("new.example.com")}, fake.policy))
	})
}

func httpsRules(host string) []*ateapipb.EgressRule {
	return []*ateapipb.EgressRule{{Https: &ateapipb.HTTPSRule{Hostnames: []string{host}}}}
}

type egressPolicyFake struct {
	ateapipb.ControlClient
	policy            *ateapipb.EgressPolicy
	actor             *ateapipb.ObjectRef
	createErr, getErr error
	created, updated  int
}

func (f *egressPolicyFake) CreateActorEgressPolicy(_ context.Context, req *ateapipb.CreateActorEgressPolicyRequest, _ ...grpc.CallOption) (*ateapipb.EgressPolicy, error) {
	f.actor = req.Actor
	if f.policy != nil {
		return nil, status.Error(codes.AlreadyExists, "exists")
	}
	f.policy = proto.CloneOf(req.EgressPolicy)
	f.policy.Metadata.Uid = "policy-uid"
	f.policy.Metadata.Version = 1
	f.created++
	return f.policy, f.createErr
}

func (f *egressPolicyFake) GetActorEgressPolicy(_ context.Context, req *ateapipb.GetActorEgressPolicyRequest, _ ...grpc.CallOption) (*ateapipb.EgressPolicy, error) {
	f.actor = req.Actor
	return f.policy, f.getErr
}

// UpdateActorEgressPolicy enforces the version precondition of the policy it
// is given, as ate-api does.
func (f *egressPolicyFake) UpdateActorEgressPolicy(_ context.Context, req *ateapipb.UpdateActorEgressPolicyRequest, _ ...grpc.CallOption) (*ateapipb.EgressPolicy, error) {
	f.actor = req.Actor
	if req.GetEgressPolicy().GetMetadata().GetVersion() != f.policy.GetMetadata().GetVersion() {
		return nil, status.Error(codes.Aborted, "version changed")
	}
	f.policy = proto.CloneOf(req.EgressPolicy)
	if f.policy.Metadata == nil {
		f.policy.Metadata = &ateapipb.ResourceMetadata{}
	}
	f.policy.Metadata.Version++
	f.updated++
	return f.policy, nil
}
