package controller

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	"github.com/google/uuid"
	kagentfake "github.com/kagent-dev/kagent/go/api/clientset/versioned/fake"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	kagentv1alpha3 "github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/dbtest"
	v2translator "github.com/kagent-dev/kagent/go/core/internal/translator"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"istio.io/istio/pkg/kube/krt"
	"istio.io/istio/pkg/kube/krt/krttest"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcilerPersistsPairInOrder(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	opts := krt.NewOptionsBuilder(stop, "test", nil)
	template := &kagentv1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "assistant", UID: "template-uid"}}
	harness := &kagentv1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "kagent", UID: "harness-uid"}}
	desiredActor := &ateapipb.ActorTemplate{Metadata: &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "assistant-kagent-revision"}}
	revision := &v2translator.Revision{AgentCard: &a2apb.AgentCard{Name: "assistant"}}
	revision.AgentCard.ProtoReflect().SetUnknown(protowire.AppendString(protowire.AppendTag(nil, 1000, protowire.BytesType), "future"))
	revisionID, err := revision.Digest()
	if err != nil {
		t.Fatal(err)
	}
	state := PairReconciliation{
		Pair:     AgentTemplateHarnessPair{AgentTemplate: template, Harness: harness},
		Revision: revision, RevisionID: revisionID, DesiredActorTemplate: desiredActor,
	}
	reconciliations := krt.NewStaticCollection(nil, []PairReconciliation{state}, opts.WithName("Reconciliations")...)
	status := kagentv1alpha3.AgentTemplateStatus{ObservedGeneration: 1, Harnesses: []kagentv1alpha3.AgentTemplateHarnessStatus{{
		Harness: "kagent", Conditions: []metav1.Condition{{Type: kagentv1alpha3.AgentTemplateConditionReady, Status: metav1.ConditionFalse}},
	}}}
	mock := krttest.NewMock(t, []any{
		template,
		krt.ObjectWithStatus[*kagentv1alpha3.AgentTemplate, kagentv1alpha3.AgentTemplateStatus]{Obj: template, Status: status},
	})
	statuses := krttest.GetMockCollection[krt.ObjectWithStatus[*kagentv1alpha3.AgentTemplate, kagentv1alpha3.AgentTemplateStatus]](mock)
	store := &fakeRuntimeRevisionStore{}
	templates := &fakeActorTemplates{}
	statusClient := kagentfake.NewSimpleClientset(template.DeepCopy()).ApiV1alpha3()
	reconciler := &Reconciler{
		collections: Collections{
			AgentTemplates:          krttest.GetMockCollection[*kagentv1alpha3.AgentTemplate](mock),
			PairRuntimeObservations: krt.NewStaticCollection[PairRuntimeObservation](nil, nil, opts.WithName("PairRuntimeObservations")...),
			Reconciliations:         reconciliations, AgentTemplateStatuses: statuses,
		},
		templates: templates, store: store, status: statusClient,
	}

	if err := reconciler.reconcilePair(context.Background(), state.ResourceName()); err != nil {
		t.Fatal(err)
	}
	if store.pair == nil {
		t.Fatal("pair was not stored")
	}
	created := templates.template
	if created == nil {
		t.Fatal("ActorTemplate was not created")
	}
	if store.revision == nil || store.markedSuccessful {
		t.Fatal("pending revision was not stored correctly")
	}

	require.True(t, proto.Equal(revision.AgentCard, store.revision.AgentCard))

	templates.template = proto.CloneOf(created)
	templates.template.Status = &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{GoldenSnapshot: &ateapipb.ExternalSnapshot{SnapshotUri: "s3://snapshots/golden"}}}
	writeErr := errors.New("database unavailable")
	store.revisionErr = writeErr
	require.ErrorIs(t, reconciler.reconcilePair(t.Context(), state.ResourceName()), writeErr)
	pending := reconciler.collections.PairRuntimeObservations.GetKey(state.ResourceName())
	require.NotNil(t, pending)
	require.Nil(t, pending.Template.GetStatus().GetGoldenSnapshotStatus().GetGoldenSnapshot(),
		"Ready must not be published before the database write succeeds")
	store.revisionErr = nil
	if err := reconciler.reconcilePair(context.Background(), state.ResourceName()); err != nil {
		t.Fatal(err)
	}
	if store.revision == nil || !store.markedSuccessful {
		t.Fatal("ready revision was not stored and marked successful")
	}
	require.Empty(t, store.retired, "active pairs must be replaced atomically by the store")
	observed := reconciler.collections.PairRuntimeObservations.GetKey(state.ResourceName())
	require.NotNil(t, observed.Template.GetStatus().GetGoldenSnapshotStatus().GetGoldenSnapshot())

	if err := reconciler.reconcileAgentTemplateStatus(context.Background(), "team-a/assistant"); err != nil {
		t.Fatal(err)
	}
	statusWrite, err := statusClient.AgentTemplates(template.Namespace).Get(context.Background(), template.Name, metav1.GetOptions{})
	if err != nil || statusWrite.Status.Harnesses[0].Conditions[0].LastTransitionTime.IsZero() {
		t.Fatal("desired status was not written with a transition time")
	}

	// A fresh reconciler must clean up observations without remembering earlier calls.
	reconciler = &Reconciler{collections: reconciler.collections, templates: templates, store: store, status: statusClient}
	state.DesiredActorTemplate = proto.CloneOf(state.DesiredActorTemplate)
	state.DesiredActorTemplate.Metadata.Name = "assistant-next-revision"
	state.Revision = &v2translator.Revision{AgentCard: &a2apb.AgentCard{Name: "updated assistant"}}
	state.RevisionID, err = state.Revision.Digest()
	require.NoError(t, err)
	templates.template = nil
	reconciliations.UpdateObject(state)
	require.NoError(t, reconciler.reconcilePair(t.Context(), state.ResourceName()))
	require.Equal(t, state.RevisionID, reconciler.collections.PairRuntimeObservations.GetKey(state.ResourceName()).RevisionID, "a new revision must replace the previous observation without waiting for GC")
	require.Len(t, reconciler.collections.PairRuntimeObservations.List(), 1)

	reconciliations.DeleteObject(state.ResourceName())
	if err := reconciler.reconcilePair(context.Background(), state.ResourceName()); err != nil {
		t.Fatal(err)
	}
	require.Empty(t, reconciler.collections.PairRuntimeObservations.List(), "pair retirement must release its observation without waiting for GC")
	if store.retired != state.ResourceName() {
		t.Fatalf("retired pair = %q, want %q", store.retired, state.ResourceName())
	}
}

func TestRuntimeRevisionGCCollectsRetiredRevisions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping database test in short mode")
	}
	for _, test := range []struct {
		name               string
		deletedBeforeError bool
		finalizeFailure    bool
	}{
		{name: "compute deletion failed"},
		{name: "compute deletion succeeded but response lost", deletedBeforeError: true},
		{name: "compute deletion succeeded but database finalization failed", finalizeFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			dsn := dbtest.StartT(ctx, t)
			dbtest.MigrateT(t, dsn, false)
			pool, err := database.Connect(ctx, &database.PostgresConfig{URL: dsn})
			require.NoError(t, err)
			t.Cleanup(pool.Close)
			store := database.NewClient(pool)
			opts := krt.NewOptionsBuilder(ctx.Done(), "test", nil)
			states := krt.NewStaticCollection[PairReconciliation](nil, nil, opts.WithName("Reconciliations")...)
			templates := &fakeActorTemplates{}
			reconciler := &Reconciler{
				collections: Collections{
					PairRuntimeObservations: krt.NewStaticCollection[PairRuntimeObservation](nil, nil, opts.WithName("PairRuntimeObservations")...),
					Reconciliations:         states,
				},
				templates: templates, store: store,
			}
			revision := &v2translator.Revision{
				AgentCard: &a2apb.AgentCard{Name: "assistant"}, Provenance: []byte("{}"), EgressDestinations: []string{},
			}
			id, err := revision.Digest()
			require.NoError(t, err)
			desired := &ateapipb.ActorTemplate{Metadata: &ateapipb.ResourceMetadata{
				Atespace: "team-a", Name: "assistant-revision", Uid: "actor-uid",
			}}
			templates.template = proto.CloneOf(desired)
			templates.template.Status = &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{
				GoldenSnapshot: &ateapipb.ExternalSnapshot{SnapshotUri: "s3://snapshots/golden"},
			}}
			state := PairReconciliation{
				Pair: AgentTemplateHarnessPair{
					AgentTemplate: &kagentv1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "assistant", UID: "template-uid"}},
					Harness:       &kagentv1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "kagent", UID: "harness-uid"}},
				},
				Revision: revision, RevisionID: id, DesiredActorTemplate: desired,
			}
			states.UpdateObject(state)
			require.NoError(t, reconciler.reconcilePair(ctx, state.ResourceName()))

			// Compile failures preserve the current UID's last successful runtime.
			state.Revision = nil
			states.UpdateObject(state)
			require.NoError(t, reconciler.reconcilePair(ctx, state.ResourceName()))
			require.Empty(t, reconciler.collections.PairRuntimeObservations.List(), "invalid preparation must release its observation before GC")
			request := &apiv1alpha1.AgentInstance{
				Id: uuid.NewString(), Creator: "alice",
				AgentTemplate: &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "assistant"},
				Harness:       &apiv1alpha1.ResourceReference{Namespace: "team-a", Name: "kagent"},
			}
			instance, _, err := store.CreateAgentInstance(ctx, request, "instance")
			require.NoError(t, err)
			require.Equal(t, id.String(), instance.GetPreparedRevision())
			require.NoError(t, store.DeleteAgentInstance(ctx, instance.GetId()))
			require.NotNil(t, templates.template)

			// An invalid replacement UID still revokes the old identity. Failed or
			// ambiguous network deletion must leave the claim available to retry.
			state.Pair.AgentTemplate = state.Pair.AgentTemplate.DeepCopy()
			state.Pair.AgentTemplate.UID = "replacement-uid"
			states.UpdateObject(state)
			deleteErr := errors.New("deletion interrupted")
			gcStore := &failingFinalizationStore{Client: store}
			if test.finalizeFailure {
				gcStore.finalizeErr = deleteErr
			} else {
				templates.deleteErr, templates.deletedBeforeError = deleteErr, test.deletedBeforeError
			}
			require.NoError(t, reconciler.reconcilePair(ctx, state.ResourceName()), "GC failures must not fail pair reconciliation")
			collector := NewRuntimeRevisionGC(gcStore, templates)
			require.ErrorIs(t, collector.collect(ctx, id.String()), deleteErr)
			if test.finalizeFailure || test.deletedBeforeError {
				require.Nil(t, templates.template)
			}
			pending, err := store.ListUnreferencedRuntimeRevisions(ctx)
			require.NoError(t, err)
			require.Len(t, pending, 1, "failed cleanup must remain discoverable after restart")
			_, err = store.GetRuntimeRevision(ctx, id.String())
			require.NoError(t, err)
			_, _, err = store.CreateAgentInstance(ctx, request, "replacement-instance")
			require.ErrorIs(t, err, database.ErrNotFound)
			templates.deleteErr = nil
			restarted := NewRuntimeRevisionGC(database.NewClient(pool), templates)
			restarted.sweep(ctx)
			require.Nil(t, templates.template)
			require.Empty(t, reconciler.collections.PairRuntimeObservations.List())
			_, err = store.GetRuntimeRevision(ctx, id.String())
			require.ErrorIs(t, err, database.ErrNotFound)
			restarted.sweep(ctx)
		})
	}
}

type failingFinalizationStore struct {
	*database.Client
	finalizeErr error
}

func (s *failingFinalizationStore) DeleteRuntimeRevision(ctx context.Context, revision, uid string) error {
	if s.finalizeErr != nil {
		return s.finalizeErr
	}
	return s.Client.DeleteRuntimeRevision(ctx, revision, uid)
}

// crashedGoldenBoot is the terminal status Substrate's template reconciler
// writes when the golden actor is observed CRASHED — the same words whether
// the workload exited or the worker pod under it was replaced.
func crashedGoldenBoot() *ateapipb.ActorTemplateStatus {
	return &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{
		ErrorMessage: "GoldenActorCrashed: golden actor crashed before its snapshot was taken",
	}}
}

func readyGoldenBoot() *ateapipb.ActorTemplateStatus {
	return &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{
		GoldenSnapshot: &ateapipb.ExternalSnapshot{SnapshotUri: "s3://snapshots/golden"},
	}}
}

// goldenBootFixture is a reconciler over fakes with a controllable clock and
// one compiled pair whose ActorTemplate the tests drive by hand.
type goldenBootFixture struct {
	reconciler *Reconciler
	templates  *fakeActorTemplates
	store      *fakeRuntimeRevisionStore
	state      PairReconciliation
	clock      time.Time
}

func newGoldenBootFixture(t *testing.T) *goldenBootFixture {
	t.Helper()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	opts := krt.NewOptionsBuilder(stop, "test", nil)
	revision := &v2translator.Revision{AgentCard: &a2apb.AgentCard{Name: "assistant"}}
	revisionID, err := revision.Digest()
	require.NoError(t, err)
	f := &goldenBootFixture{
		templates: &fakeActorTemplates{}, store: &fakeRuntimeRevisionStore{},
		state: PairReconciliation{
			Pair: AgentTemplateHarnessPair{
				AgentTemplate: &kagentv1alpha3.AgentTemplate{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "assistant", UID: "template-uid"}},
				Harness:       &kagentv1alpha3.Harness{ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "kagent", UID: "harness-uid"}},
			},
			Revision: revision, RevisionID: revisionID,
			DesiredActorTemplate: &ateapipb.ActorTemplate{Metadata: &ateapipb.ResourceMetadata{Atespace: "team-a", Name: "assistant-kagent-revision"}},
		},
		clock: time.Date(2026, time.September, 12, 16, 14, 38, 0, time.UTC),
	}
	f.reconciler = &Reconciler{
		collections: Collections{
			PairRuntimeObservations: krt.NewStaticCollection[PairRuntimeObservation](nil, nil, opts.WithName("PairRuntimeObservations")...),
			Reconciliations:         krt.NewStaticCollection(nil, []PairReconciliation{f.state}, opts.WithName("Reconciliations")...),
		},
		templates: f.templates, store: f.store,
		now: func() time.Time { return f.clock },
	}
	return f
}

func (f *goldenBootFixture) reconcile(t *testing.T) {
	t.Helper()
	require.NoError(t, f.reconciler.reconcilePair(t.Context(), f.state.ResourceName()))
}

func (f *goldenBootFixture) observation(t *testing.T) PairRuntimeObservation {
	t.Helper()
	observed := f.reconciler.collections.PairRuntimeObservations.GetKey(f.state.ResourceName())
	require.NotNil(t, observed, "the pair must stay observed")
	return *observed
}

// The status the pure graph derives from the observation, as the AgentTemplate
// would publish it.
func (f *goldenBootFixture) readyCondition(t *testing.T) metav1.Condition {
	t.Helper()
	state := f.state
	state.ObservedActorTemplate = f.observation(t).Template
	state.GoldenBootRetry, state.Failure = goldenBootOutcome(f.observation(t))
	ready := apimeta.FindStatusCondition(statusForPair(state, 1, "").Conditions, kagentv1alpha3.AgentTemplateConditionReady)
	require.NotNil(t, ready)
	return *ready
}

func TestReconcilerStartsACrashedGoldenBootOver(t *testing.T) {
	f := newGoldenBootFixture(t)
	f.reconcile(t)
	require.Equal(t, 1, f.templates.created)
	require.Equal(t, "actor-uid", f.store.revision.ActorTemplateUID)

	// The worker pool rolled under the golden actor: Substrate marks the boot
	// crashed, terminally, and never touches the template again.
	f.templates.template.Status = crashedGoldenBoot()
	f.reconcile(t)
	observed := f.observation(t)
	require.Equal(t, 0, observed.GoldenBootRetries)
	require.Equal(t, f.clock.Add(goldenBootRetryBaseDelay), observed.RetryGoldenBootAt, "the first sight of a crash schedules its retry")
	require.Empty(t, f.templates.deleted, "a crash is not started over before its backoff")
	ready := f.readyCondition(t)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, "ActorTemplateRetrying", ready.Reason)
	require.Contains(t, ready.Message, "golden boot 1 of 6 failed (GoldenActorCrashed: golden actor crashed before its snapshot was taken)")

	f.clock = f.clock.Add(goldenBootRetryBaseDelay - time.Second)
	f.reconcile(t)
	require.Empty(t, f.templates.deleted)
	require.Equal(t, observed.RetryGoldenBootAt, f.observation(t).RetryGoldenBootAt, "the deadline holds across observations")

	f.clock = f.clock.Add(time.Second)
	f.reconcile(t)
	require.Equal(t, []string{"actor-uid"}, f.templates.deleted, "the crashed template and its golden actor are removed")
	require.Equal(t, 2, f.templates.created, "the desired template is created again")
	require.Equal(t, "actor-uid-2", f.templates.template.GetMetadata().GetUid())
	require.Nil(t, f.templates.template.GetStatus(), "the new boot starts clean")
	require.Equal(t, "actor-uid-2", f.store.revision.ActorTemplateUID, "the revision follows the new template so GC can tell them apart")
	require.False(t, f.store.markedSuccessful)
	observed = f.observation(t)
	require.Equal(t, 1, observed.GoldenBootRetries)
	require.True(t, observed.RetryGoldenBootAt.IsZero())
	require.Equal(t, "ActorTemplatePending", f.readyCondition(t).Reason)

	// The second boot lands on the settled pool and is snapshotted: Ready,
	// with no change to the AgentTemplate or the Harness.
	f.templates.template.Status = readyGoldenBoot()
	f.reconcile(t)
	require.True(t, f.store.markedSuccessful)
	require.Equal(t, 1, f.observation(t).GoldenBootRetries, "the count stays with the revision")
	ready = f.readyCondition(t)
	require.Equal(t, metav1.ConditionTrue, ready.Status)
	require.Equal(t, "Ready", ready.Reason)
}

func TestReconcilerStopsStartingAPersistentlyCrashingGoldenBootOver(t *testing.T) {
	f := newGoldenBootFixture(t)
	f.reconcile(t)
	// A workload that exits on every boot crashes the same way a rolled pool
	// does; the budget tells them apart in the end.
	var delays []time.Duration
	for retry := range maxGoldenBootRetries {
		f.templates.template.Status = crashedGoldenBoot()
		f.reconcile(t)
		observed := f.observation(t)
		require.Equal(t, retry, observed.GoldenBootRetries)
		delays = append(delays, observed.RetryGoldenBootAt.Sub(f.clock))
		f.clock = observed.RetryGoldenBootAt
		f.reconcile(t)
		require.Len(t, f.templates.deleted, retry+1)
		require.Equal(t, retry+2, f.templates.created)
	}
	require.Equal(t, []time.Duration{20 * time.Second, 40 * time.Second, 80 * time.Second, 2 * time.Minute, 2 * time.Minute}, delays, "the backoff doubles up to its cap")

	f.templates.template.Status = crashedGoldenBoot()
	f.reconcile(t)
	f.clock = f.clock.Add(time.Hour)
	f.reconcile(t)
	require.Len(t, f.templates.deleted, maxGoldenBootRetries, "the budget is spent: the last crash stands")
	observed := f.observation(t)
	require.True(t, observed.RetryGoldenBootAt.IsZero())
	retry, failure := goldenBootOutcome(observed)
	require.Nil(t, retry)
	require.NotNil(t, failure)
	require.Equal(t, "ActorTemplateFailed", failure.Reason)
	require.Equal(t, "GoldenActorCrashed: golden actor crashed before its snapshot was taken (6 golden boots crashed)", failure.Message)
	ready := f.readyCondition(t)
	require.Equal(t, metav1.ConditionFalse, ready.Status)
	require.Equal(t, "ActorTemplateFailed", ready.Reason)
}

// A crash the resume reported with its cause, and an invalid template, are
// final at once: no retry repairs them, and a misconfigured template keeps
// failing fast.
func TestReconcilerDoesNotStartAnAttributedGoldenBootFailureOver(t *testing.T) {
	for _, message := range []string{
		"GoldenActorCrashed: workflow failed at step CallAteletRestore: rpc error: code = Internal desc = actor ate-golden/uid crashed: " +
			"while creating \"kagent\" OCI bundle: FAILED_GET_EXTERNAL_OBJECT: MANIFEST_UNKNOWN: manifest unknown",
		"GoldenActorInvalid: rpc error: code = InvalidArgument desc = containers[0].image: unsupported reference",
	} {
		f := newGoldenBootFixture(t)
		f.reconcile(t)
		f.templates.template.Status = &ateapipb.ActorTemplateStatus{GoldenSnapshotStatus: &ateapipb.GoldenSnapshotStatus{ErrorMessage: message}}
		f.reconcile(t)
		f.clock = f.clock.Add(time.Hour)
		f.reconcile(t)
		require.Empty(t, f.templates.deleted, "a failure no retry can repair is not started over: %s", message)
		require.Equal(t, 1, f.templates.created)
		observed := f.observation(t)
		require.True(t, observed.RetryGoldenBootAt.IsZero())
		retry, failure := goldenBootOutcome(observed)
		require.Nil(t, retry)
		require.Equal(t, "ActorTemplateFailed", failure.Reason)
		require.Equal(t, message, failure.Message)
		require.Equal(t, "ActorTemplateFailed", f.readyCondition(t).Reason)
	}
}

// A crashed template replaced outside the reconciler's own restart — a
// recreate that failed halfway, an operator's deletion — still counts against
// the revision's budget once the reconciler observes the replacement.
func TestReconcilerCountsAReplacedCrashedGoldenBoot(t *testing.T) {
	f := newGoldenBootFixture(t)
	f.reconcile(t)
	f.templates.template.Status = crashedGoldenBoot()
	f.reconcile(t)
	f.templates.template = nil
	f.reconcile(t)
	require.Empty(t, f.templates.deleted)
	require.Equal(t, 2, f.templates.created)
	observed := f.observation(t)
	require.Equal(t, 1, observed.GoldenBootRetries)
	require.True(t, observed.RetryGoldenBootAt.IsZero())
}

type fakeActorTemplates struct {
	template           *ateapipb.ActorTemplate
	deleteErr          error
	deletedBeforeError bool
	created            int
	deleted            []string
}

func (f *fakeActorTemplates) EnsureAtespace(context.Context, string) error { return nil }

// GetActorTemplate answers with a copy, as a call over the wire does, so a
// test that changes the stored template between calls changes what the next
// call observes and nothing the reconciler already holds.
func (f *fakeActorTemplates) GetActorTemplate(context.Context, string, string) (*ateapipb.ActorTemplate, error) {
	if f.template == nil {
		return nil, status.Error(codes.NotFound, "not found")
	}
	return fakeActorTemplateCopy(f.template, f.template.GetMetadata().GetUid()), nil
}

// CreateActorTemplate hands out a fresh UID per creation, the way Substrate
// does, so a template created again after a deletion is told apart. Like
// GetActorTemplate it answers with a copy of what it stores.
func (f *fakeActorTemplates) CreateActorTemplate(_ context.Context, template *ateapipb.ActorTemplate) (*ateapipb.ActorTemplate, error) {
	f.created++
	uid := "actor-uid"
	if f.created > 1 {
		uid += fmt.Sprintf("-%d", f.created)
	}
	f.template = fakeActorTemplateCopy(template, uid)
	return fakeActorTemplateCopy(f.template, uid), nil
}

// fakeActorTemplateCopy copies a template through its getters, the way
// substrate.ActorTemplateSpecEqual reads one. The desired template belongs to
// the KRT graph, which compares it with reflect.DeepEqual on its own
// goroutine; a proto.Clone or Marshal of it would be its first ProtoReflect,
// a write the race detector rightly reports against that comparison.
func fakeActorTemplateCopy(template *ateapipb.ActorTemplate, uid string) *ateapipb.ActorTemplate {
	return &ateapipb.ActorTemplate{
		Metadata: &ateapipb.ResourceMetadata{
			Atespace: template.GetMetadata().GetAtespace(), Name: template.GetMetadata().GetName(), Uid: uid,
		},
		WorkerSelector: template.GetWorkerSelector(), Containers: template.GetContainers(), Volumes: template.GetVolumes(),
		SnapshotsConfig: template.GetSnapshotsConfig(), SandboxConfig: template.GetSandboxConfig(), Resources: template.GetResources(),
		Status: template.GetStatus(),
	}
}

func (f *fakeActorTemplates) DeleteActorTemplate(_ context.Context, _, _, uid string) error {
	if f.deleteErr != nil {
		if f.deletedBeforeError {
			f.template = nil
		}
		return f.deleteErr
	}
	f.deleted = append(f.deleted, uid)
	f.template = nil
	return nil
}

type fakeRuntimeRevisionStore struct {
	pair             *database.AgentTemplateHarnessPair
	revision         *database.RuntimeRevision
	markedSuccessful bool
	retired          string
	revisionErr      error
	pairErr          error
}

func (s *fakeRuntimeRevisionStore) UpsertAgentTemplateHarnessPair(_ context.Context, pair database.AgentTemplateHarnessPair) error {
	s.pair = &pair
	return s.pairErr
}

func (s *fakeRuntimeRevisionStore) RecordRuntimeRevision(_ context.Context, revision database.RuntimeRevision, ready bool) error {
	if s.revisionErr != nil {
		return s.revisionErr
	}
	s.revision = &revision
	s.markedSuccessful = ready
	return nil
}

func (s *fakeRuntimeRevisionStore) RetirePairIdentities(_ context.Context, namespace, template, harness string, except *database.AgentTemplateHarnessPair) error {
	if except == nil {
		s.retired = namespace + "/" + template + "/" + harness
	}
	return nil
}

func TestReconcilerUpdatesModelConfigStatusOnSecretHashChange(t *testing.T) {
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	opts := krt.NewOptionsBuilder(stop, "test", nil)

	modelConfig := &kagentv1alpha3.ModelConfig{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "model", Generation: 1},
		Spec: kagentv1alpha3.ModelConfigSpec{
			Model:        "gpt-5",
			Provider:     kagentv1alpha3.ModelProviderOpenAI,
			APIKeySecret: "credentials",
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "credentials"},
		Data:       map[string][]byte{"key": []byte("initial-secret")},
	}

	mock := krttest.NewMock(t, []any{modelConfig})
	modelConfigs := krttest.GetMockCollection[*kagentv1alpha3.ModelConfig](mock)
	secrets := krt.NewStaticCollection(nil, []*corev1.Secret{secret}, opts.WithName("Secrets")...)
	configMaps := krttest.GetMockCollection[*corev1.ConfigMap](mock)
	modelConfigStatuses, resolvedModelConfigs := newModelConfigReconciliations(modelConfigs, configMaps, secrets, opts)

	collections := Collections{
		ModelConfigs:          modelConfigs,
		Secrets:               secrets,
		ConfigMaps:            configMaps,
		ModelConfigStatuses:   modelConfigStatuses,
		ResolvedModelConfigs:  resolvedModelConfigs,
		AgentTemplates:        krttest.GetMockCollection[*kagentv1alpha3.AgentTemplate](mock),
		Reconciliations:       krttest.GetMockCollection[PairReconciliation](mock),
		AgentTemplateStatuses: krttest.GetMockCollection[krt.ObjectWithStatus[*kagentv1alpha3.AgentTemplate, kagentv1alpha3.AgentTemplateStatus]](mock),
	}

	statusClient := kagentfake.NewSimpleClientset(modelConfig.DeepCopy()).ApiV1alpha3()
	reconciler := newReconciler(
		collections,
		&fakeActorTemplates{},
		&fakeRuntimeRevisionStore{},
		statusClient,
	)

	go reconciler.Run(stop)

	var initialUpdate *kagentv1alpha3.ModelConfig
	var err error
	require.Eventually(t, func() bool {
		initialUpdate, err = statusClient.ModelConfigs(modelConfig.Namespace).Get(context.Background(), modelConfig.Name, metav1.GetOptions{})
		return err == nil && initialUpdate.Status.SecretHash != ""
	}, 3*time.Second, 10*time.Millisecond)

	if len(initialUpdate.Status.Conditions) != 2 {
		t.Fatalf("expected 2 conditions in status, got: %+v", initialUpdate.Status.Conditions)
	}
	if initialUpdate.Status.Conditions[0].LastTransitionTime.IsZero() || initialUpdate.Status.Conditions[1].LastTransitionTime.IsZero() {
		t.Fatal("expected LastTransitionTime to be set on ModelConfig conditions")
	}

	initialHash := initialUpdate.Status.SecretHash

	secrets.UpdateObject(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "team-a", Name: "credentials"},
		Data:       map[string][]byte{"key": []byte("updated-secret")},
	})

	var updatedMC *kagentv1alpha3.ModelConfig
	require.Eventually(t, func() bool {
		updatedMC, err = statusClient.ModelConfigs(modelConfig.Namespace).Get(context.Background(), modelConfig.Name, metav1.GetOptions{})
		return err == nil && updatedMC.Status.SecretHash != initialHash
	}, 3*time.Second, 10*time.Millisecond)
}

func TestReconciliationQueueRetriesWithBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var attempts atomic.Int32
		queue := newReconciliationQueue("test-retries", func(any) error {
			attempts.Add(1)
			return errors.New("database unavailable")
		})
		go queue.Run(ctx.Done())
		queue.Add("team-a/assistant/kagent")
		synctest.Wait()
		require.EqualValues(t, 1, attempts.Load())
		time.Sleep(time.Second - time.Nanosecond)
		synctest.Wait()
		require.EqualValues(t, 1, attempts.Load(), "errors must back off before retrying")
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		require.EqualValues(t, 2, attempts.Load())
		time.Sleep(3 * time.Minute)
		synctest.Wait()
		require.EqualValues(t, 10, attempts.Load(), "persistent failures must exhaust a finite budget")
		time.Sleep(time.Minute)
		synctest.Wait()
		require.EqualValues(t, 10, attempts.Load())
		queue.Add("team-a/assistant/kagent")
		synctest.Wait()
		require.EqualValues(t, 11, attempts.Load(), "new graph events must still enqueue work")
		cancel()
		require.NoError(t, queue.WaitForClose(time.Second))
	})
}
