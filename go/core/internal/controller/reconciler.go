package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agent-substrate/substrate/pkg/proto/ateapipb"
	kagentclient "github.com/kagent-dev/kagent/go/api/clientset/versioned/typed/api/v1alpha3"
	kagentv1alpha3 "github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/kagent/go/core/internal/database"
	"github.com/kagent-dev/kagent/go/core/internal/substrate"
	v2translator "github.com/kagent-dev/kagent/go/core/internal/translator"
	byotranslator "github.com/kagent-dev/kagent/go/core/internal/translator/byo"
	claudetranslator "github.com/kagent-dev/kagent/go/core/internal/translator/claude"
	codextranslator "github.com/kagent-dev/kagent/go/core/internal/translator/codex"
	kagenttranslator "github.com/kagent-dev/kagent/go/core/internal/translator/kagent"
	"github.com/kagent-dev/kagent/go/pkg/logging"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"istio.io/istio/pkg/kube/controllers"
	"istio.io/istio/pkg/kube/krt"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/workqueue"
)

// PairReconciliation is the complete desired and observed state for one
// AgentTemplate/Harness pair. Failure is data so invalid pairs still produce
// status instead of disappearing from the graph.
type PairReconciliation struct {
	Pair                  AgentTemplateHarnessPair
	Revision              *v2translator.Revision
	Warnings              []string
	RevisionID            v2translator.RevisionID
	DesiredActorTemplate  *ateapipb.ActorTemplate
	ObservedActorTemplate *ateapipb.ActorTemplate
	// GoldenBootRetry is set while the observed golden boot crashed and will
	// be started over; the pair stays pending rather than failed.
	GoldenBootRetry *GoldenBootRetry
	Failure         *ReconciliationFailure
}

func (r PairReconciliation) ResourceName() string { return r.Pair.ResourceName() }

// ReconciliationFailure identifies the condition stage blocked by a pair.
type ReconciliationFailure struct {
	Condition string
	Reason    string
	Message   string
}

// GoldenBootRetry describes a crashed golden boot the reconciler starts over.
type GoldenBootRetry struct {
	// Attempt counts the golden boots of the revision that crashed so far.
	Attempt int
	Message string
}

// A golden boot whose actor Substrate found crashed without a cause is started
// over. Substrate's template reconciler writes exactly this message when it
// observes the golden actor CRASHED and the resume did not report why; the
// Actor carries no crash reason, so a worker pod replaced under the golden
// actor (the pool rolling during a Harness change) is indistinguishable from
// a workload that exited after it came up. A crash the resume itself reported
// carries its cause after the reason — an image the registry refuses, a
// container with no runnable process — and stays final, so a misconfigured
// template still fails fast. The retries are bounded so a workload that dies
// on every boot still fails, and spaced out so a rolling pool has time to
// settle.
const (
	goldenActorCrashedUnattributed = "GoldenActorCrashed: golden actor crashed before its snapshot was taken"
	maxGoldenBootRetries           = 5
	goldenBootRetryBaseDelay       = 20 * time.Second
	goldenBootRetryMaxDelay        = 2 * time.Minute
)

// goldenBootCrashed reports whether a golden snapshot status carries the
// unattributed crash that is worth starting over, as opposed to a crash with a
// cause or another terminal failure (an invalid template, an actor someone
// else paused or deleted), which no retry can repair.
func goldenBootCrashed(golden *ateapipb.GoldenSnapshotStatus) bool {
	return golden.GetErrorMessage() == goldenActorCrashedUnattributed
}

// goldenBootRetryDelay doubles with every retry of a revision, from the base
// delay up to the cap.
func goldenBootRetryDelay(retries int) time.Duration {
	delay := goldenBootRetryBaseDelay << retries
	if delay <= 0 || delay > goldenBootRetryMaxDelay {
		return goldenBootRetryMaxDelay
	}
	return delay
}

// goldenBootOutcome reads a failed golden boot from the pair's observation: a
// crash within the retry budget is retried, anything else fails the pair.
func goldenBootOutcome(observed PairRuntimeObservation) (*GoldenBootRetry, *ReconciliationFailure) {
	golden := observed.Template.GetStatus().GetGoldenSnapshotStatus()
	message := golden.GetErrorMessage()
	if message == "" {
		return nil, nil
	}
	if goldenBootCrashed(golden) {
		if observed.GoldenBootRetries < maxGoldenBootRetries {
			return &GoldenBootRetry{Attempt: observed.GoldenBootRetries + 1, Message: message}, nil
		}
		message = fmt.Sprintf("%s (%d golden boots crashed)", message, observed.GoldenBootRetries+1)
	}
	return nil, &ReconciliationFailure{Condition: kagentv1alpha3.AgentTemplateConditionReady, Reason: "ActorTemplateFailed", Message: message}
}

func newPairReconciliations(
	pairs krt.Collection[AgentTemplateHarnessPair],
	collections v2translator.Collections,
	pairRuntimeObservations krt.Collection[PairRuntimeObservation],
	opts krt.OptionsBuilder,
) krt.Collection[PairReconciliation] {
	return krt.NewCollection(pairs, func(ctx krt.HandlerContext, pair AgentTemplateHarnessPair) *PairReconciliation {
		state := &PairReconciliation{Pair: pair}
		compilation, err := v2translator.NewCompiler(ctx, collections, map[v2translator.HarnessType]v2translator.HarnessCompiler{
			v2translator.HarnessTypeKagent: kagenttranslator.NewCompiler(ctx, collections),
			v2translator.HarnessTypeCodex:  codextranslator.NewCompiler(ctx, collections),
			v2translator.HarnessTypeClaude: claudetranslator.NewCompiler(ctx, collections),
			v2translator.HarnessTypeBYO:    byotranslator.NewCompiler(ctx, collections),
		}).CompileAgentTemplate(context.Background(), pair.Harness, pair.AgentTemplate)
		if err != nil {
			condition, reason := kagentv1alpha3.AgentTemplateConditionResolvedRefs, "ReferenceResolutionFailed"
			var validation *v2translator.ValidationError
			if errors.As(err, &validation) {
				condition, reason = kagentv1alpha3.AgentTemplateConditionCompatible, "UnsupportedConfiguration"
			}
			state.Failure = &ReconciliationFailure{Condition: condition, Reason: reason, Message: err.Error()}
			return state
		}
		revision := &compilation.Revision
		state.Revision = revision
		state.Warnings = append([]string(nil), compilation.Warnings...)
		state.RevisionID, err = revision.Digest()
		if err != nil {
			state.Failure = &ReconciliationFailure{Condition: kagentv1alpha3.AgentTemplateConditionCompatible, Reason: "RevisionInvalid", Message: err.Error()}
			return state
		}

		workerKey := types.NamespacedName{Namespace: revision.Namespace, Name: revision.WorkerPoolName}
		if krt.FetchOne(ctx, collections.WorkerPools, krt.FilterObjectName(workerKey)) == nil {
			state.Failure = &ReconciliationFailure{Condition: kagentv1alpha3.AgentTemplateConditionResolvedRefs, Reason: "WorkerPoolNotFound", Message: fmt.Sprintf("WorkerPool %q not found", workerKey.String())}
			return state
		}
		state.DesiredActorTemplate, err = substrate.ActorTemplateForRevision(revision, state.RevisionID)
		if err != nil {
			state.Failure = &ReconciliationFailure{Condition: kagentv1alpha3.AgentTemplateConditionCompatible, Reason: "ActorTemplateInvalid", Message: err.Error()}
			return state
		}

		observed := krt.FetchOne(ctx, pairRuntimeObservations, krt.FilterKey(pair.ResourceName()))
		if observed == nil || observed.RevisionID != state.RevisionID {
			return state
		}
		state.ObservedActorTemplate = (*observed).Template
		if !substrate.ActorTemplateSpecEqual(state.ObservedActorTemplate, state.DesiredActorTemplate) {
			state.Failure = &ReconciliationFailure{
				Condition: kagentv1alpha3.AgentTemplateConditionReady,
				Reason:    "ActorTemplateConflict",
				Message:   "existing immutable ActorTemplate differs from the compiled revision",
			}
		} else {
			state.GoldenBootRetry, state.Failure = goldenBootOutcome(*observed)
		}
		return state
	}, opts.WithName("PairReconciliations")...)
}

// runtimeRevisionStore is the controller's narrow view of the shared database.
// Substrate owns ActorTemplates; the database retains revisions while a pair
// or an AgentInstance or checkpoint references them.
type runtimeRevisionStore interface {
	UpsertAgentTemplateHarnessPair(context.Context, database.AgentTemplateHarnessPair) error
	RecordRuntimeRevision(context.Context, database.RuntimeRevision, bool) error
	RetirePairIdentities(ctx context.Context, namespace, templateName, harnessName string, except *database.AgentTemplateHarnessPair) error
}

type actorTemplateClient interface {
	EnsureAtespace(context.Context, string) error
	GetActorTemplate(context.Context, string, string) (*ateapipb.ActorTemplate, error)
	CreateActorTemplate(context.Context, *ateapipb.ActorTemplate) (*ateapipb.ActorTemplate, error)
	DeleteActorTemplate(ctx context.Context, atespace, name, uid string) error
}

// Reconciler is the side-effect boundary for the pure KRT graph. Collection
// handlers enqueue stable keys; retries always read the latest derived state.
type Reconciler struct {
	collections Collections
	templates   actorTemplateClient
	store       runtimeRevisionStore
	status      kagentclient.ApiV1alpha3Interface
	// now is the clock the golden-boot retries are scheduled by; nil means
	// time.Now.
	now func() time.Time

	pairs                      controllers.Queue
	agentTemplateStatuses      controllers.Queue
	modelConfigStatuses        controllers.Queue
	pairHandler                krt.HandlerRegistration
	agentTemplateStatusHandler krt.HandlerRegistration
	modelConfigStatusHandler   krt.HandlerRegistration
}

// NewReconciler creates the Kubernetes and database write boundary. Run starts
// its queues after the registered KRT handlers have received initial state.
func NewReconciler(config *rest.Config, collections Collections, store runtimeRevisionStore, templates actorTemplateClient) (*Reconciler, error) {
	statusClient, err := kagentclient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kagent status client: %w", err)
	}
	return newReconciler(collections, templates, store, statusClient), nil
}

func newReconciler(
	collections Collections,
	templates actorTemplateClient,
	store runtimeRevisionStore,
	status kagentclient.ApiV1alpha3Interface,
) *Reconciler {
	r := &Reconciler{
		collections: collections,
		templates:   templates,
		store:       store,
		status:      status,
	}
	r.pairs = newReconciliationQueue("v2-agent-template-pairs", func(item any) error {
		return r.reconcilePair(context.Background(), item.(string))
	})
	r.agentTemplateStatuses = newReconciliationQueue("v2-agent-template-status", func(item any) error {
		return r.reconcileAgentTemplateStatus(context.Background(), item.(string))
	})
	r.modelConfigStatuses = newReconciliationQueue("v2-model-config-status", func(item any) error {
		return r.reconcileModelConfigStatus(context.Background(), item.(string))
	})

	r.pairHandler = collections.Reconciliations.Register(func(event krt.Event[PairReconciliation]) {
		r.pairs.Add(krt.GetKey(event.Latest()))
	})
	r.agentTemplateStatusHandler = collections.AgentTemplateStatuses.Register(func(event krt.Event[krt.ObjectWithStatus[*kagentv1alpha3.AgentTemplate, kagentv1alpha3.AgentTemplateStatus]]) {
		status := event.Latest()
		if apiequality.Semantic.DeepEqual(statusWithTransitionTimes(status.Status, status.Obj.Status), status.Obj.Status) {
			return
		}
		r.agentTemplateStatuses.Add(status.ResourceName())
	})
	r.modelConfigStatusHandler = collections.ModelConfigStatuses.Register(func(event krt.Event[krt.ObjectWithStatus[*kagentv1alpha3.ModelConfig, kagentv1alpha3.ModelConfigStatus]]) {
		status := event.Latest()
		if apiequality.Semantic.DeepEqual(modelConfigStatusWithTransitionTimes(status.Status, status.Obj.Status), status.Obj.Status) {
			return
		}
		r.modelConfigStatuses.Add(status.ResourceName())
	})
	return r
}

// Ten attempts span about 2.5 minutes. Each queue owns its backoff state;
// fresh graph events can enqueue work again after an error budget is exhausted.
func newReconciliationQueue(name string, reconcile func(any) error) controllers.Queue {
	return controllers.NewQueue(name, controllers.WithGenericReconciler(reconcile),
		controllers.WithMaxAttempts(10),
		controllers.WithRateLimiter(workqueue.NewTypedItemExponentialFailureRateLimiter[any](time.Second, 30*time.Second)),
	)
}

// Run waits for the graph boundary to observe initial state, then processes
// pair and status writes until stop closes.
func (r *Reconciler) Run(stop <-chan struct{}) {
	if !r.pairHandler.WaitUntilSynced(stop) || !r.agentTemplateStatusHandler.WaitUntilSynced(stop) || !r.modelConfigStatusHandler.WaitUntilSynced(stop) {
		r.pairs.ShutDownEarly()
		r.agentTemplateStatuses.ShutDownEarly()
		r.modelConfigStatuses.ShutDownEarly()
		return
	}
	go r.pollPendingTemplates(stop)
	go r.agentTemplateStatuses.Run(stop)
	go r.modelConfigStatuses.Run(stop)
	r.pairs.Run(stop)
}

func (r *Reconciler) pollPendingTemplates(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			for _, state := range r.collections.Reconciliations.List() {
				golden := state.ObservedActorTemplate.GetStatus().GetGoldenSnapshotStatus()
				if state.Failure == nil && golden.GetGoldenSnapshot() == nil {
					r.pairs.Add(state.ResourceName())
				}
			}
		}
	}
}

func (r *Reconciler) Start(ctx context.Context) error {
	r.Run(ctx.Done())
	return nil
}

func (r *Reconciler) NeedLeaderElection() bool { return true }

func (r *Reconciler) reconcilePair(ctx context.Context, key string) error {
	state := r.collections.Reconciliations.GetKey(key)
	if observation := r.collections.PairRuntimeObservations.GetKey(key); observation != nil &&
		(state == nil || state.Revision == nil || observation.RevisionID != state.RevisionID) {
		r.collections.PairRuntimeObservations.DeleteObject(key)
	}
	if state == nil {
		parts := strings.Split(key, "/")
		if len(parts) != 3 {
			return fmt.Errorf("invalid AgentTemplate/Harness pair key %q", key)
		}
		if err := r.store.RetirePairIdentities(ctx, parts[0], parts[1], parts[2], nil); err != nil {
			return fmt.Errorf("retire AgentTemplate/Harness pair %s: %w", key, err)
		}
		return nil
	}
	pair := database.AgentTemplateHarnessPair{
		Namespace: state.Pair.AgentTemplate.Namespace, AgentTemplateName: state.Pair.AgentTemplate.Name,
		AgentTemplateUID: string(state.Pair.AgentTemplate.UID), HarnessName: state.Pair.Harness.Name,
		HarnessUID: string(state.Pair.Harness.UID), DesiredRevision: state.RevisionID.String(),
	}
	if state.Revision == nil || state.RevisionID.IsZero() {
		// Bad inputs must not destroy the current identity's last-good runtime.
		// A recreated object at this name must still retire the previous UID.
		if err := r.store.RetirePairIdentities(ctx, pair.Namespace, pair.AgentTemplateName, pair.HarnessName, &pair); err != nil {
			return fmt.Errorf("retire replaced AgentTemplate/Harness pair %s: %w", key, err)
		}
		return nil
	}
	// Store the desired edge before creating compute so a concurrent collector
	// cannot mistake the revision for abandoned state.
	if err := r.store.UpsertAgentTemplateHarnessPair(ctx, pair); err != nil {
		if errors.Is(err, database.ErrObjectDeleting) {
			// A desired digest may be awaiting cleanup from an earlier identity.
			// Clearing the observation makes KRT derive a pending pair, which
			// the pending-template poll retries until GC finishes.
			r.collections.PairRuntimeObservations.DeleteObject(key)
			return nil
		}
		return fmt.Errorf("store AgentTemplate/Harness pair %s: %w", key, err)
	}
	if state.Failure != nil {
		return nil
	}
	desiredRef := state.DesiredActorTemplate.GetMetadata()
	observed, err := r.templates.GetActorTemplate(ctx, desiredRef.GetAtespace(), desiredRef.GetName())
	if status.Code(err) == codes.NotFound {
		if err := r.templates.EnsureAtespace(ctx, desiredRef.GetAtespace()); err != nil {
			return fmt.Errorf("ensure Atespace %s: %w", desiredRef.GetAtespace(), err)
		}
		observed, err = r.templates.CreateActorTemplate(ctx, state.DesiredActorTemplate)
		if status.Code(err) == codes.AlreadyExists {
			observed, err = r.templates.GetActorTemplate(ctx, desiredRef.GetAtespace(), desiredRef.GetName())
		}
	}
	if err != nil {
		return fmt.Errorf("reconcile ActorTemplate %s/%s: %w", desiredRef.GetAtespace(), desiredRef.GetName(), err)
	}
	if !substrate.ActorTemplateSpecEqual(observed, state.DesiredActorTemplate) {
		r.observeActorTemplate(*state, observed, goldenBootSchedule{})
		return nil
	}
	schedule := r.goldenBootSchedule(key, *state, observed)
	if schedule.due {
		if observed, err = r.restartGoldenBoot(ctx, *state, observed); err != nil {
			return err
		}
		schedule.retries++
		schedule.retryAt = time.Time{}
	}

	revision := database.RuntimeRevision{
		Revision: state.RevisionID.String(), Namespace: pair.Namespace, AgentTemplateName: pair.AgentTemplateName,
		AgentTemplateUID: pair.AgentTemplateUID, HarnessName: pair.HarnessName, HarnessUID: pair.HarnessUID,
		SourceSnapshot: state.Revision.Provenance, AgentCard: state.Revision.AgentCard,
		EgressDestinations:    state.Revision.EgressDestinations,
		ActorTemplateAtespace: observed.GetMetadata().GetAtespace(), ActorTemplateName: observed.GetMetadata().GetName(), ActorTemplateUID: observed.GetMetadata().GetUid(),
	}
	ready := observed.GetStatus().GetGoldenSnapshotStatus().GetGoldenSnapshot() != nil
	if err := r.store.RecordRuntimeRevision(ctx, revision, ready); err != nil {
		return fmt.Errorf("store runtime revision %s: %w", state.RevisionID, err)
	}
	// This observation drives Kubernetes Ready status on a separate queue.
	// Publish it only after instance creation can select the persisted revision.
	r.observeActorTemplate(*state, observed, schedule)
	return nil
}

// Observations belong to the pair's current preparation, independently of how
// long instances or checkpoints keep its old runtime alive in the database.
// The golden-boot retry bookkeeping travels with the template it describes.
func (r *Reconciler) observeActorTemplate(state PairReconciliation, template *ateapipb.ActorTemplate, schedule goldenBootSchedule) {
	r.collections.PairRuntimeObservations.ConditionalUpdateObject(PairRuntimeObservation{
		AgentTemplateName: state.Pair.AgentTemplate.Name,
		HarnessName:       state.Pair.Harness.Name,
		RevisionID:        state.RevisionID,
		Template:          template,
		GoldenBootRetries: schedule.retries,
		RetryGoldenBootAt: schedule.retryAt,
	})
}

// goldenBootSchedule is a revision's retry bookkeeping for the next
// observation — how many golden boots were started over and when the observed
// crash may be — and whether that crash is due to be started over now.
type goldenBootSchedule struct {
	retries int
	retryAt time.Time
	due     bool
}

// goldenBootSchedule derives the retry bookkeeping of the observed
// ActorTemplate from the pair's previous observation of the same revision. A
// crashed boot replaced by another template counts as one retry, however it
// was replaced; a crash seen for the first time gets its backoff deadline; a
// crash past its deadline is due, unless the budget is spent.
func (r *Reconciler) goldenBootSchedule(key string, state PairReconciliation, observed *ateapipb.ActorTemplate) goldenBootSchedule {
	var schedule goldenBootSchedule
	if previous := r.collections.PairRuntimeObservations.GetKey(key); previous != nil && previous.RevisionID == state.RevisionID {
		schedule.retries = previous.GoldenBootRetries
		if previous.Template.GetMetadata().GetUid() == observed.GetMetadata().GetUid() {
			schedule.retryAt = previous.RetryGoldenBootAt
		} else if goldenBootCrashed(previous.Template.GetStatus().GetGoldenSnapshotStatus()) {
			schedule.retries++
		}
	}
	if !goldenBootCrashed(observed.GetStatus().GetGoldenSnapshotStatus()) || schedule.retries >= maxGoldenBootRetries {
		schedule.retryAt = time.Time{}
		return schedule
	}
	now := r.clock()
	if schedule.retryAt.IsZero() {
		schedule.retryAt = now.Add(goldenBootRetryDelay(schedule.retries))
		return schedule
	}
	schedule.due = !now.Before(schedule.retryAt)
	return schedule
}

// restartGoldenBoot replaces a crashed ActorTemplate with a fresh copy of the
// desired one. ActorTemplates are immutable and a failed golden snapshot is
// terminal in Substrate, so starting the boot over means deleting the
// template (and its golden actor) and creating it again; nothing runs on a
// revision without a golden snapshot, so no instance loses its template.
func (r *Reconciler) restartGoldenBoot(ctx context.Context, state PairReconciliation, crashed *ateapipb.ActorTemplate) (*ateapipb.ActorTemplate, error) {
	ref := crashed.GetMetadata()
	logging.FromContext(ctx).InfoContext(ctx, "starting a crashed golden boot over",
		"pair", state.ResourceName(), "revision", state.RevisionID.String(),
		"actor_template", ref.GetAtespace()+"/"+ref.GetName(), "error", crashed.GetStatus().GetGoldenSnapshotStatus().GetErrorMessage())
	if err := r.templates.DeleteActorTemplate(ctx, ref.GetAtespace(), ref.GetName(), ref.GetUid()); err != nil {
		return nil, fmt.Errorf("delete crashed ActorTemplate %s/%s: %w", ref.GetAtespace(), ref.GetName(), err)
	}
	created, err := r.templates.CreateActorTemplate(ctx, state.DesiredActorTemplate)
	if err != nil {
		return nil, fmt.Errorf("recreate ActorTemplate %s/%s: %w", ref.GetAtespace(), ref.GetName(), err)
	}
	return created, nil
}

func (r *Reconciler) clock() time.Time {
	if r.now == nil {
		return time.Now()
	}
	return r.now()
}

func (r *Reconciler) reconcileAgentTemplateStatus(ctx context.Context, key string) error {
	desired := r.collections.AgentTemplateStatuses.GetKey(key)
	template := r.collections.AgentTemplates.GetKey(key)
	if desired == nil || template == nil {
		return nil
	}
	updated := (*template).DeepCopy()
	updated.Status = statusWithTransitionTimes(desired.Status, updated.Status)
	if apiequality.Semantic.DeepEqual(updated.Status, (*template).Status) {
		return nil
	}
	if _, err := r.status.AgentTemplates(updated.Namespace).UpdateStatus(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update AgentTemplate %s status: %w", key, err)
	}
	return nil
}

func (r *Reconciler) reconcileModelConfigStatus(ctx context.Context, key string) error {
	desired := r.collections.ModelConfigStatuses.GetKey(key)
	modelConfig := r.collections.ModelConfigs.GetKey(key)
	if desired == nil || modelConfig == nil {
		return nil
	}
	updated := (*modelConfig).DeepCopy()
	updated.Status = modelConfigStatusWithTransitionTimes(desired.Status, updated.Status)
	if apiequality.Semantic.DeepEqual(updated.Status, (*modelConfig).Status) {
		return nil
	}
	if _, err := r.status.ModelConfigs(updated.Namespace).UpdateStatus(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update ModelConfig %s status: %w", key, err)
	}
	return nil
}

func statusWithTransitionTimes(desired, current kagentv1alpha3.AgentTemplateStatus) kagentv1alpha3.AgentTemplateStatus {
	desired.Harnesses = append([]kagentv1alpha3.AgentTemplateHarnessStatus(nil), desired.Harnesses...)
	for harnessIndex := range desired.Harnesses {
		desiredHarness := &desired.Harnesses[harnessIndex]
		desiredHarness.Conditions = append([]metav1.Condition(nil), desiredHarness.Conditions...)
		var currentHarness *kagentv1alpha3.AgentTemplateHarnessStatus
		for index := range current.Harnesses {
			if current.Harnesses[index].Harness == desiredHarness.Harness {
				currentHarness = &current.Harnesses[index]
				break
			}
		}
		for conditionIndex := range desiredHarness.Conditions {
			condition := &desiredHarness.Conditions[conditionIndex]
			if currentHarness != nil {
				if previous := apimeta.FindStatusCondition(currentHarness.Conditions, condition.Type); previous != nil &&
					previous.Status == condition.Status && previous.Reason == condition.Reason &&
					previous.Message == condition.Message && previous.ObservedGeneration == condition.ObservedGeneration {
					condition.LastTransitionTime = previous.LastTransitionTime
					continue
				}
			}
			condition.LastTransitionTime = metav1.Now()
		}
	}
	return desired
}

func modelConfigStatusWithTransitionTimes(desired, current kagentv1alpha3.ModelConfigStatus) kagentv1alpha3.ModelConfigStatus {
	desired.Conditions = append([]metav1.Condition(nil), desired.Conditions...)
	for conditionIndex := range desired.Conditions {
		condition := &desired.Conditions[conditionIndex]
		if previous := apimeta.FindStatusCondition(current.Conditions, condition.Type); previous != nil &&
			previous.Status == condition.Status && previous.Reason == condition.Reason &&
			previous.Message == condition.Message && previous.ObservedGeneration == condition.ObservedGeneration {
			condition.LastTransitionTime = previous.LastTransitionTime
			continue
		}
		condition.LastTransitionTime = metav1.Now()
	}
	return desired
}
