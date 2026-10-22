package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	kagentv1alpha3 "github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/egress"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	"github.com/kagent-dev/kagent/go/core/internal/translator"
)

// fakeEgressApplier records the instances given their grants; fail names the
// instances whose Actor is unavailable.
type fakeEgressApplier struct {
	mu      sync.Mutex
	applied []string
	fail    map[string]bool
}

func (f *fakeEgressApplier) ApplyEgress(_ context.Context, target database.RuntimeEgressTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, target.ID)
	if f.fail[target.ID] {
		return errors.New("actor unavailable")
	}
	return nil
}

// pageTargets is the keyset page ListRuntimeEgressSourceTargets returns.
func pageTargets(targets []database.RuntimeEgressTarget, after string, limit int) []database.RuntimeEgressTarget {
	var page []database.RuntimeEgressTarget
	for _, target := range targets {
		if target.ID > after && len(page) < limit {
			page = append(page, target)
		}
	}
	return page
}

type egressTargetStore struct {
	targets []database.RuntimeEgressTarget
	listErr error
	calls   int
}

func (s *egressTargetStore) ListRuntimeEgressSourceTargets(_ context.Context, _ database.RuntimeEgressSource, after string, limit int) ([]database.RuntimeEgressTarget, error) {
	s.calls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return pageTargets(s.targets, after, limit), nil
}

func TestApplySourceEgressReachesEveryActorPastFailures(t *testing.T) {
	store := &egressTargetStore{}
	for i := range 250 {
		store.targets = append(store.targets, database.RuntimeEgressTarget{ID: fmt.Sprintf("s-%03d", i)})
	}
	actors := &fakeEgressApplier{fail: map[string]bool{"s-007": true, "s-180": true}}

	err := applySourceEgress(t.Context(), store, actors, database.RuntimeEgressSource{Kind: database.RuntimeEgressAgent, Namespace: "team-a", Name: "helper"})

	require.ErrorContains(t, err, "instance s-007")
	require.ErrorContains(t, err, "instance s-180")
	require.Equal(t, 3, store.calls, "every page is read")
	slices.Sort(actors.applied)
	require.Len(t, actors.applied, 250, "one unavailable actor cannot hold the others' revocation")
}

func TestApplySourceEgressReportsAListFailure(t *testing.T) {
	store := &egressTargetStore{listErr: errors.New("database unavailable")}
	err := applySourceEgress(t.Context(), store, &fakeEgressApplier{}, database.RuntimeEgressSource{Kind: database.RuntimeEgressSandbox, Namespace: "team-a", Name: "scratch"})
	require.ErrorContains(t, err, "list the actors of sandbox team-a/scratch")
}

func TestAdmittedGrants(t *testing.T) {
	open := &egress.CallerCredential{PassthroughRecorded: true}
	tickets := "https://tickets.example.com:443"
	wiki := "https://wiki.example.com:443"
	removed := "https://old.example.com:443"
	conflicting := "tls://models.example.com:443"
	base := egress.Access{Destinations: []string{"https://models.example.com:443"}}

	requested := egress.Access{Destinations: []string{tickets, conflicting}}
	conflict := grantConflict(requested, base, open)
	require.Contains(t, conflict, "conflict with the runtime's own access")

	previous := egress.Access{Destinations: []string{tickets, removed}}
	require.Equal(t, egress.Access{Destinations: []string{tickets}}, admittedGrants(requested, previous, base, open, conflict),
		"removals apply at once, additions wait for the conflict")

	fitting := egress.Access{Destinations: []string{tickets, wiki}}
	require.Empty(t, grantConflict(fitting, base, open))
	require.Equal(t, fitting, admittedGrants(fitting, previous, base, open, ""))
}

func TestEgressPoliciesCondition(t *testing.T) {
	applied := egressPoliciesCondition(kagentv1alpha3.AgentConditionEgressPoliciesApplied, 3, nil, "")
	require.Equal(t, metav1.ConditionTrue, applied.Status)
	require.Equal(t, kagentv1alpha3.EgressPoliciesReasonApplied, applied.Reason)
	require.EqualValues(t, 3, applied.ObservedGeneration)

	issue := translator.EgressGrantIssue{Policy: "tickets", Reason: "PolicyNotFound", Message: "not found"}
	missing := egressPoliciesCondition(kagentv1alpha3.AgentConditionEgressPoliciesApplied, 3, []translator.EgressGrantIssue{issue}, "")
	require.Equal(t, metav1.ConditionFalse, missing.Status)
	require.Equal(t, "PolicyNotFound", missing.Reason)
	require.Equal(t, "tickets: not found", missing.Message)

	conflict := egressPoliciesCondition(kagentv1alpha3.AgentConditionEgressPoliciesApplied, 3, []translator.EgressGrantIssue{issue}, strings.Repeat("x", 5000))
	require.Equal(t, kagentv1alpha3.EgressPoliciesReasonConflict, conflict.Reason)
	require.True(t, strings.HasPrefix(conflict.Message, "new grants are held until the conflict is resolved; tickets: not found; first conflict: "))
	require.Len(t, conflict.Message, 4096)
}

type sandboxEgressFakes struct {
	actor    *ateapipb.Actor
	getErr   error
	revision database.SandboxRevision
	grants   egress.Access
	applied  *ateapipb.EgressPolicy
}

func (f *sandboxEgressFakes) GetActor(context.Context, string, string) (*ateapipb.Actor, error) {
	return f.actor, f.getErr
}

func (f *sandboxEgressFakes) ApplyActorEgressPolicy(_ context.Context, _, _ string, update func(*ateapipb.EgressPolicy) (*ateapipb.EgressPolicy, error)) error {
	policy, err := update(nil)
	f.applied = policy
	return err
}

func (f *sandboxEgressFakes) GetSandboxRevision(context.Context, string) (*database.SandboxRevision, error) {
	return &f.revision, nil
}

func (f *sandboxEgressFakes) GetRuntimeEgressGrant(context.Context, string) (egress.Access, error) {
	return f.grants, nil
}

func TestSandboxEgressAppliesLiveGrants(t *testing.T) {
	target := database.RuntimeEgressTarget{ID: "SBX1", Revision: "rev", Atespace: "team-a", TemplateName: "sandbox-rev"}
	matching := &ateapipb.Actor{
		Metadata:      &ateapipb.ResourceMetadata{Atespace: "team-a", Name: substrate.ActorName("SBX1"), Uid: "actor-uid"},
		ActorTemplate: &ateapipb.ObjectRef{Atespace: "team-a", Name: "sandbox-rev"},
	}
	grants := egress.Access{Destinations: []string{"https://tickets.example.com:443"}}

	t.Run("applies base and grants", func(t *testing.T) {
		fakes := &sandboxEgressFakes{actor: matching, grants: grants}
		require.NoError(t, sandboxEgress{store: fakes, actors: fakes}.ApplyEgress(t.Context(), target))
		require.NotNil(t, fakes.applied)
		want, err := substrate.SandboxEgressPolicy("team-a", egress.Access{}, grants)
		require.NoError(t, err)
		require.Equal(t, want.String(), fakes.applied.String())
	})
	t.Run("revocation leaves the base", func(t *testing.T) {
		fakes := &sandboxEgressFakes{actor: matching}
		require.NoError(t, sandboxEgress{store: fakes, actors: fakes}.ApplyEgress(t.Context(), target))
		want, err := substrate.SandboxEgressPolicy("team-a", egress.Access{}, egress.Access{})
		require.NoError(t, err)
		require.Equal(t, want.String(), fakes.applied.String())
	})
	t.Run("a deleted actor has nothing to change", func(t *testing.T) {
		fakes := &sandboxEgressFakes{getErr: status.Error(codes.NotFound, "gone"), grants: grants}
		require.NoError(t, sandboxEgress{store: fakes, actors: fakes}.ApplyEgress(t.Context(), target))
		require.Nil(t, fakes.applied)
	})
	t.Run("another template's actor is left alone", func(t *testing.T) {
		other := &ateapipb.Actor{Metadata: matching.Metadata, ActorTemplate: &ateapipb.ObjectRef{Atespace: "team-a", Name: "sandbox-other"}}
		fakes := &sandboxEgressFakes{actor: other, grants: grants}
		require.NoError(t, sandboxEgress{store: fakes, actors: fakes}.ApplyEgress(t.Context(), target))
		require.Nil(t, fakes.applied)
	})
}
