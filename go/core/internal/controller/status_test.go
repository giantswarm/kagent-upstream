package controller

import (
	"context"
	"testing"
	"time"

	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	kagentfake "github.com/kagent-dev/kagent/go/api/clientset/versioned/fake"
	kagentv1alpha3 "github.com/kagent-dev/kagent/go/api/v1alpha3"
	v2translator "github.com/kagent-dev/kagent/go/core/internal/translator"
	"github.com/stretchr/testify/require"
	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/kube/krt/krttest"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestStatusForPairPublishesCompilationWarnings(t *testing.T) {
	warnings := []string{"partial MCP selection is not enforced"}
	state := PairReconciliation{
		Pair:       AgentTemplateHarnessPair{Harness: &kagentv1alpha3.Harness{}},
		Revision:   &v2translator.Revision{},
		Warnings:   warnings,
		RevisionID: v2translator.RevisionID{1},
	}
	status := statusForPair(state, 1, "")
	if len(status.Warnings) != 1 || status.Warnings[0] != warnings[0] {
		t.Fatalf("warnings = %v, want %v", status.Warnings, warnings)
	}
	warnings[0] = "changed"
	if status.Warnings[0] == warnings[0] {
		t.Fatal("status aliases mutable compilation warnings")
	}
}

func harnessPairState(template, harness string, mutate func(*PairReconciliation)) PairReconciliation {
	state := PairReconciliation{
		Pair: AgentTemplateHarnessPair{
			AgentTemplate: &kagentv1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: template}},
			Harness:       &kagentv1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: harness}},
		},
		Revision:              &v2translator.Revision{},
		RevisionID:            v2translator.RevisionID{1},
		ObservedActorTemplate: &ateapipb.ActorTemplate{},
	}
	if mutate != nil {
		mutate(&state)
	}
	return state
}

func booted(state *PairReconciliation) {
	state.ObservedActorTemplate = &ateapipb.ActorTemplate{Status: &ateapipb.ActorTemplateStatus{
		GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{GoldenTag: &ateapipb.ObjectRef{Atespace: "ate-golden", Name: "golden"}},
	}}
}

func bootFailed(state *PairReconciliation) {
	state.Failure = &ReconciliationFailure{Condition: kagentv1alpha3.AgentTemplateConditionReady, Reason: "ActorTemplateFailed", Message: "ImagePullBackOff"}
}

func TestHarnessReadyCondition(t *testing.T) {
	pool := types.NamespacedName{Namespace: "team-a", Name: "gvisor"}
	pair := func(template string, mutate func(*PairReconciliation)) PairReconciliation {
		return harnessPairState(template, "kagent", mutate)
	}
	tests := []struct {
		name        string
		poolFound   bool
		pairs       []PairReconciliation
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{name: "missing worker pool", pairs: []PairReconciliation{pair("a", booted)},
			wantStatus: metav1.ConditionFalse, wantReason: "WorkerPoolNotFound", wantMessage: `WorkerPool "team-a/gvisor" not found`},
		{name: "no agent templates", poolFound: true,
			wantStatus: metav1.ConditionUnknown, wantReason: "NoAgentTemplates", wantMessage: "the Harness admits no AgentTemplate yet"},
		{name: "boot pending", poolFound: true, pairs: []PairReconciliation{pair("a", nil)},
			wantStatus: metav1.ConditionUnknown, wantReason: "BootPending", wantMessage: "waiting for the golden boot of AgentTemplate team-a/a"},
		{name: "booted", poolFound: true, pairs: []PairReconciliation{pair("a", booted)},
			wantStatus: metav1.ConditionTrue, wantReason: "Booted", wantMessage: "golden boot of AgentTemplate team-a/a succeeded"},
		{name: "failed boot", poolFound: true, pairs: []PairReconciliation{pair("a", bootFailed)},
			wantStatus: metav1.ConditionFalse, wantReason: "ActorTemplateFailed", wantMessage: "AgentTemplate team-a/a: ImagePullBackOff"},
		{name: "crashed boot retrying", poolFound: true, pairs: []PairReconciliation{pair("a", func(s *PairReconciliation) {
			s.GoldenBootRetry = &GoldenBootRetry{Attempt: 1, Message: "GoldenActorCrashed: exit 1"}
		})},
			wantStatus: metav1.ConditionFalse, wantReason: "ActorTemplateRetrying", wantMessage: "AgentTemplate team-a/a: golden boot 1 of 6 failed (GoldenActorCrashed: exit 1); starting it over"},
		{name: "one booted template outweighs another's failure", poolFound: true, pairs: []PairReconciliation{pair("a", bootFailed), pair("b", booted)},
			wantStatus: metav1.ConditionTrue, wantReason: "Booted", wantMessage: "golden boot of AgentTemplate team-a/b succeeded"},
		{name: "a failure outweighs a pending boot", poolFound: true, pairs: []PairReconciliation{pair("a", nil), pair("b", bootFailed)},
			wantStatus: metav1.ConditionFalse, wantReason: "ActorTemplateFailed", wantMessage: "AgentTemplate team-a/b: ImagePullBackOff"},
		{name: "template failures before the boot say nothing about the harness", poolFound: true, pairs: []PairReconciliation{
			pair("a", func(s *PairReconciliation) {
				s.Revision = nil
				s.Failure = &ReconciliationFailure{Condition: kagentv1alpha3.AgentTemplateConditionCompatible, Reason: "UnsupportedConfiguration"}
			}),
			pair("b", func(s *PairReconciliation) {
				s.Failure = &ReconciliationFailure{Condition: kagentv1alpha3.AgentTemplateConditionCompatible, Reason: "ActorTemplateInvalid"}
			}),
		},
			wantStatus: metav1.ConditionUnknown, wantReason: "NoAgentTemplates", wantMessage: "the Harness admits no AgentTemplate yet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := harnessReadyCondition(3, pool, tt.poolFound, tt.pairs)
			want := metav1.Condition{
				Type: kagentv1alpha3.HarnessConditionTypeReady, Status: tt.wantStatus, Reason: tt.wantReason, Message: tt.wantMessage, ObservedGeneration: 3,
			}
			require.Equal(t, want, got)
		})
	}
}

func TestReconcilerWritesHarnessReady(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	opts := krt.NewOptionsBuilder(stop, "test", nil)

	harness := &kagentv1alpha3.Harness{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "kagent", Generation: 2},
		Spec: kagentv1alpha3.HarnessSpec{Substrate: kagentv1alpha3.HarnessSubstratePolicy{
			WorkerPoolRef: corev1.LocalObjectReference{Name: "gvisor"},
		}},
		Status: kagentv1alpha3.HarnessStatus{Capabilities: &kagentv1alpha3.HarnessCapabilities{Version: "v1"}},
	}
	mock := krttest.NewMock(t, []any{
		harness,
		&atev1alpha1.WorkerPool{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "gvisor"}},
		harnessPairState("assistant", "kagent", booted),
		harnessPairState("assistant", "codex", bootFailed),
	})
	harnesses := krttest.GetMockCollection[*kagentv1alpha3.Harness](mock)
	states := krttest.GetMockCollection[PairReconciliation](mock)
	collections := Collections{
		Harnesses:             harnesses,
		Reconciliations:       krt.NewStaticCollection[PairReconciliation](nil, nil, opts.WithName("NoReconciliations")...),
		HarnessStatuses:       newHarnessStatuses(harnesses, krttest.GetMockCollection[*atev1alpha1.WorkerPool](mock), states, opts),
		AgentTemplateStatuses: krttest.GetMockCollection[krt.ObjectWithStatus[*kagentv1alpha3.AgentTemplate, kagentv1alpha3.AgentTemplateStatus]](mock),
		ModelConfigStatuses:   krttest.GetMockCollection[krt.ObjectWithStatus[*kagentv1alpha3.ModelConfig, kagentv1alpha3.ModelConfigStatus]](mock),
	}
	statusClient := kagentfake.NewSimpleClientset(harness.DeepCopy()).ApiV1alpha3()
	go newReconciler(collections, &fakeActorTemplates{}, &fakeRuntimeRevisionStore{}, statusClient).Run(stop)

	var updated *kagentv1alpha3.Harness
	require.Eventually(t, func() bool {
		var err error
		updated, err = statusClient.Harnesses("team-a").Get(context.Background(), "kagent", metav1.GetOptions{})
		return err == nil && apimeta.IsStatusConditionTrue(updated.Status.Conditions, kagentv1alpha3.HarnessConditionTypeReady)
	}, 3*time.Second, 10*time.Millisecond)
	ready := apimeta.FindStatusCondition(updated.Status.Conditions, kagentv1alpha3.HarnessConditionTypeReady)
	require.Equal(t, "Booted", ready.Reason)
	require.False(t, ready.LastTransitionTime.IsZero())
	require.Equal(t, int64(2), updated.Status.ObservedGeneration)
	require.Equal(t, harness.Status.Capabilities, updated.Status.Capabilities, "the status write keeps the capabilities")
}
