package substrate

import (
	"context"
	"errors"
	"testing"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
)

type egressPolicyStoreFunc func(context.Context, string) (egress.Access, error)

func (f egressPolicyStoreFunc) GetRuntimeEgressPolicy(ctx context.Context, revision string) (egress.Access, error) {
	return f(ctx, revision)
}

var _ egress.PolicyStore = egressPolicyStoreFunc(nil)

func TestEnsureActorEgressPolicyUpdatesCurrentGrants(t *testing.T) {
	fake := &egressPolicyFake{createErr: context.DeadlineExceeded}
	access := egress.Access{Destinations: []string{"https://api.example.com:443"}}
	client := &Client{ControlClient: fake, cfg: Config{EgressPolicies: egressPolicyStoreFunc(func(_ context.Context, revision string) (egress.Access, error) {
		require.Equal(t, "pinned-revision", revision)
		return access, nil
	})}}
	reconcile := func() error {
		return client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", "pinned-revision")
	}
	require.ErrorIs(t, reconcile(), context.DeadlineExceeded)
	require.NotNil(t, fake.policy, "the server committed before the response was lost")
	fake.createErr = nil
	require.NoError(t, reconcile())
	require.NoError(t, reconcile())
	require.Equal(t, 1, fake.created)
	require.Zero(t, fake.updated)
	require.Equal(t, actorRef("team-a", "actor"), fake.actor)

	access = egress.Access{}
	require.NoError(t, reconcile())
	require.Empty(t, fake.policy.Rules, "removing the grant revokes the existing actor")
	require.Equal(t, 1, fake.updated)
	access = egress.Access{Destinations: []string{"https://api.example.com:443"}, Credentials: []egress.Credential{{Origin: "https://api.example.com:443", Header: "authorization", URI: "ate-secret://k8s.io/default/team-a/auth/token", Prefix: "Bearer "}}}
	require.NoError(t, reconcile())
	require.Equal(t, "Bearer ", fake.policy.Rules[0].GetHttps().GetEffects().GetReplaceHeaders()[0].Prefix)
	access.Credentials[0].URI = "ate-secret://k8s.io/default/team-a/rotated/token"
	require.NoError(t, reconcile())
	require.Equal(t, access.Credentials[0].URI, fake.policy.Rules[0].GetHttps().GetEffects().GetReplaceHeaders()[0].CredentialUri)
	fake.getErr = status.Error(codes.Unavailable, "unavailable")
	require.Equal(t, codes.Unavailable, status.Code(reconcile()))
}

func TestEnsureActorEgressPolicyReloadsAfterConflict(t *testing.T) {
	fake := &egressPolicyFake{}
	access := egress.Access{}
	client := &Client{ControlClient: fake, cfg: Config{EgressPolicies: egressPolicyStoreFunc(func(context.Context, string) (egress.Access, error) { return access, nil })}}
	reconcile := func() error {
		return client.EnsureActorEgressPolicy(t.Context(), "team-a", "actor", "revision")
	}
	require.NoError(t, reconcile())
	access.Destinations = []string{"https://revoked.example.com:443"}
	fake.beforeUpdate = func() {
		// Another reconciliation committed revocation after this attempt read the
		// backend version. Retrying must resolve current grants, not replay the allow.
		access = egress.Access{}
		fake.policy.Metadata.Version++
	}
	require.NoError(t, reconcile())
	require.Empty(t, fake.policy.Rules)
	require.Zero(t, fake.updated)
}

type egressPolicyFake struct {
	ateapipb.ControlClient
	policy            *ateapipb.EgressPolicy
	actor             *ateapipb.ObjectRef
	createErr, getErr error
	created, updated  int
	beforeUpdate      func()
	afterCreate       func()
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
	if f.afterCreate != nil {
		f.afterCreate()
	}
	return proto.CloneOf(f.policy), f.createErr
}

func (f *egressPolicyFake) GetActorEgressPolicy(_ context.Context, req *ateapipb.GetActorEgressPolicyRequest, _ ...grpc.CallOption) (*ateapipb.EgressPolicy, error) {
	f.actor = req.Actor
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.policy == nil {
		return nil, status.Error(codes.NotFound, "missing")
	}
	return proto.CloneOf(f.policy), nil
}

func (f *egressPolicyFake) UpdateActorEgressPolicy(_ context.Context, req *ateapipb.UpdateActorEgressPolicyRequest, _ ...grpc.CallOption) (*ateapipb.EgressPolicy, error) {
	if f.beforeUpdate != nil {
		f.beforeUpdate()
		f.beforeUpdate = nil
	}
	if req.EgressPolicy.Metadata.Version != f.policy.Metadata.Version || req.EgressPolicy.Metadata.Uid != f.policy.Metadata.Uid {
		return nil, status.Error(codes.Aborted, "version conflict")
	}
	f.policy = proto.CloneOf(req.EgressPolicy)
	f.policy.Metadata.Version++
	f.updated++
	return proto.CloneOf(f.policy), nil
}

func TestEnsureActorEgressPolicyCatchesUpdateDuringCreation(t *testing.T) {
	fake := &egressPolicyFake{}
	access := egress.Access{Destinations: []string{"https://revoked.example.com:443"}}
	client := &Client{ControlClient: fake, cfg: Config{EgressPolicies: egressPolicyStoreFunc(func(context.Context, string) (egress.Access, error) { return access, nil })}}
	fake.afterCreate = func() {
		// The controller published revocation, but its actor discovery ran before
		// creation. There is no concurrent Substrate write to cause a CAS conflict.
		access = egress.Access{}
	}
	err := client.EnsureActorEgressPolicy(t.Context(), "team", "actor", "revision")
	require.NoError(t, err)
	require.Empty(t, fake.policy.Rules)
	require.Equal(t, 1, fake.created)
	require.Equal(t, 1, fake.updated)
}

func TestEnsureActorEgressPolicyRequiresPersistedPolicy(t *testing.T) {
	fake := &egressPolicyFake{}
	unavailable := errors.New("intended policy unavailable")
	client := &Client{ControlClient: fake, cfg: Config{EgressPolicies: egressPolicyStoreFunc(func(context.Context, string) (egress.Access, error) { return egress.Access{}, unavailable })}}
	err := client.EnsureActorEgressPolicy(t.Context(), "team", "actor", "revision")
	require.ErrorIs(t, err, unavailable)
	require.Zero(t, fake.created)
	require.Zero(t, fake.updated)
}

type sessionEgressStoreFunc func(context.Context, string) ([]*apiv1.SessionEgressDestination, error)

func (f sessionEgressStoreFunc) GetSessionEgress(ctx context.Context, sessionID string) ([]*apiv1.SessionEgressDestination, error) {
	return f(ctx, sessionID)
}

// A session's actor gets the egress its session was admitted with beside its
// revision's; an actor that runs no session gets only the revision's.
func TestEnsureActorEgressPolicyAddsTheSessionsEgress(t *testing.T) {
	const sessionID = "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	access := egress.Access{Destinations: []string{"https://api.example.com:443"}}
	sessionEgress := []*apiv1.SessionEgressDestination{{Origin: "https://git.example.com:443", Authentication: &apiv1.SessionEgressAuthentication{
		Header: "authorization", Prefix: "Basic ", Source: &apiv1.SessionEgressAuthentication_CredentialUri{CredentialUri: "ate-secret://credentials.example.com/workspaces/git"},
	}}}
	var asked []string
	store := sessionEgressStoreFunc(func(_ context.Context, id string) ([]*apiv1.SessionEgressDestination, error) {
		asked = append(asked, id)
		return sessionEgress, nil
	})
	fake := &egressPolicyFake{}
	client := &Client{ControlClient: fake, cfg: Config{
		EgressPolicies: egressPolicyStoreFunc(func(context.Context, string) (egress.Access, error) { return access, nil }),
		SessionEgress:  store,
	}}
	require.NoError(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", ActorName(sessionID), "revision"))
	require.Equal(t, []string{sessionID}, asked)
	require.Len(t, fake.policy.Rules, 2)
	require.Equal(t, []string{"git.example.com"}, fake.policy.Rules[1].GetHttps().GetHostnames())
	require.Equal(t, "ate-secret://credentials.example.com/workspaces/git", fake.policy.Rules[1].GetHttps().GetEffects().GetReplaceHeaders()[0].GetCredentialUri())

	other := &egressPolicyFake{}
	client.ControlClient = other
	require.NoError(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", "sandbox-actor", "revision"))
	require.Len(t, asked, 1, "an actor that runs no session has no session egress")
	require.Len(t, other.policy.Rules, 1)

	unavailable := errors.New("store unavailable")
	client.cfg.SessionEgress = sessionEgressStoreFunc(func(context.Context, string) ([]*apiv1.SessionEgressDestination, error) { return nil, unavailable })
	require.ErrorIs(t, client.EnsureActorEgressPolicy(t.Context(), "team-a", ActorName(sessionID), "revision"), unavailable)
}
