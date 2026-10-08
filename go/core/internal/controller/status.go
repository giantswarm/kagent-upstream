package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	atev1alpha1 "github.com/agent-substrate/substrate/pkg/api/v1alpha1"
	kagentv1alpha3 "github.com/kagent-dev/kagent/go/api/v1alpha3"
	"istio.io/istio/pkg/kube/krt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func newAgentTemplateStatuses(templates krt.Collection[*kagentv1alpha3.AgentTemplate], states krt.Collection[PairReconciliation], opts krt.OptionsBuilder) krt.StatusCollection[*kagentv1alpha3.AgentTemplate, kagentv1alpha3.AgentTemplateStatus] {
	statesByTemplate := krt.NewIndex(states, "statesByAgentTemplate", func(state PairReconciliation) []string {
		return []string{state.Pair.AgentTemplate.Namespace + "/" + state.Pair.AgentTemplate.Name}
	})
	statuses, _ := krt.NewStatusManyCollection(templates, func(ctx krt.HandlerContext, template *kagentv1alpha3.AgentTemplate) (*kagentv1alpha3.AgentTemplateStatus, []PairReconciliation) {
		pairStates := statesByTemplate.Fetch(ctx, template.Namespace+"/"+template.Name)
		slices.SortFunc(pairStates, func(a, b PairReconciliation) int {
			return strings.Compare(a.Pair.Harness.Name, b.Pair.Harness.Name)
		})
		previous := make(map[string]string, len(template.Status.Harnesses))
		for _, status := range template.Status.Harnesses {
			previous[status.Harness] = status.LatestSuccessfulRevision
		}
		statuses := make([]kagentv1alpha3.AgentTemplateHarnessStatus, 0, len(pairStates))
		for _, state := range pairStates {
			statuses = append(statuses, statusForPair(state, template.Generation, previous[state.Pair.Harness.Name]))
		}
		return &kagentv1alpha3.AgentTemplateStatus{ObservedGeneration: template.Generation, Harnesses: statuses}, nil
	}, opts.WithName("AgentTemplateStatuses")...)
	return statuses
}

func statusForPair(state PairReconciliation, generation int64, latestSuccessful string) kagentv1alpha3.AgentTemplateHarnessStatus {
	status := kagentv1alpha3.AgentTemplateHarnessStatus{
		Harness: state.Pair.Harness.Name, DesiredRevision: state.desiredRevision(), LatestSuccessfulRevision: latestSuccessful,
	}
	status.Warnings = append([]string(nil), state.Warnings...)
	setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionAccepted, metav1.ConditionTrue, "Accepted", "Harness admission selector matches the AgentTemplate")
	if state.Failure != nil {
		if state.Failure.Condition != kagentv1alpha3.AgentTemplateConditionResolvedRefs {
			setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionResolvedRefs, metav1.ConditionTrue, "Resolved", "All runtime references resolved")
		}
		if state.Failure.Condition == kagentv1alpha3.AgentTemplateConditionReady {
			setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionCompatible, metav1.ConditionTrue, "Compatible", "Resolved configuration is compatible with the Harness")
		}
		setPairFailure(&status, generation, state.Failure)
		return status
	}
	setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionResolvedRefs, metav1.ConditionTrue, "Resolved", "All runtime references resolved")
	setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionCompatible, metav1.ConditionTrue, "Compatible", "Resolved configuration is compatible with the Harness")
	if state.ObservedActorTemplate.GetStatus().GetGoldenSnapshotStatus().GetGoldenTag() == nil {
		if retry := state.GoldenBootRetry; retry != nil {
			message := goldenBootFailedMessage(retry.Attempt, retry.Message, "starting it over")
			setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionReady, metav1.ConditionFalse, "ActorTemplateRetrying", message)
			return status
		}
		setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionReady, metav1.ConditionFalse, "ActorTemplatePending", "waiting for the ActorTemplate golden snapshot")
		return status
	}
	status.LatestSuccessfulRevision = state.RevisionID.String()
	setPairCondition(&status, generation, kagentv1alpha3.AgentTemplateConditionReady, metav1.ConditionTrue, "Ready", "ActorTemplate golden snapshot is ready")
	return status
}

func setPairFailure(status *kagentv1alpha3.AgentTemplateHarnessStatus, generation int64, failure *ReconciliationFailure) {
	stages := []string{kagentv1alpha3.AgentTemplateConditionResolvedRefs, kagentv1alpha3.AgentTemplateConditionCompatible, kagentv1alpha3.AgentTemplateConditionReady}
	failed := false
	for _, stage := range stages {
		if stage == failure.Condition {
			setPairCondition(status, generation, stage, metav1.ConditionFalse, failure.Reason, failure.Message)
			failed = true
			continue
		}
		if failed {
			setPairCondition(status, generation, stage, metav1.ConditionFalse, "Blocked", "blocked by "+failure.Condition)
		}
	}
}

func setPairCondition(status *kagentv1alpha3.AgentTemplateHarnessStatus, generation int64, conditionType string, conditionStatus metav1.ConditionStatus, reason, message string) {
	status.Conditions = append(status.Conditions, metav1.Condition{
		Type: conditionType, Status: conditionStatus, Reason: reason, Message: message, ObservedGeneration: generation,
	})
}

func newHarnessStatuses(
	harnesses krt.Collection[*kagentv1alpha3.Harness],
	workerPools krt.Collection[*atev1alpha1.WorkerPool],
	states krt.Collection[PairReconciliation],
	opts krt.OptionsBuilder,
) krt.StatusCollection[*kagentv1alpha3.Harness, kagentv1alpha3.HarnessStatus] {
	statesByHarness := krt.NewIndex(states, "statesByHarness", func(state PairReconciliation) []string {
		return []string{state.Pair.Harness.Namespace + "/" + state.Pair.Harness.Name}
	})
	statuses, _ := krt.NewStatusManyCollection(harnesses, func(ctx krt.HandlerContext, harness *kagentv1alpha3.Harness) (*kagentv1alpha3.HarnessStatus, []PairReconciliation) {
		pool := types.NamespacedName{Namespace: harness.Namespace, Name: harness.Spec.Substrate.WorkerPoolRef.Name}
		poolFound := krt.FetchOne(ctx, workerPools, krt.FilterObjectName(pool)) != nil
		pairStates := statesByHarness.Fetch(ctx, harness.Namespace+"/"+harness.Name)
		return &kagentv1alpha3.HarnessStatus{
			ObservedGeneration: harness.Generation,
			Conditions:         []metav1.Condition{harnessReadyCondition(harness.Generation, pool, poolFound, pairStates)},
		}, nil
	}, opts.WithName("HarnessStatuses")...)
	return statuses
}

// harnessReadyCondition reads a Harness's readiness from its WorkerPool and
// the golden boots of the AgentTemplates it admits. Every pair's revision
// boots the Harness's workload, so one successful golden boot proves the
// Harness boots; a failed boot fails it only while no pair booted, since a
// single AgentTemplate's own configuration can fail its boot too. Without a
// booted or failed pair nothing has tried the workload yet and readiness is
// Unknown.
func harnessReadyCondition(generation int64, pool types.NamespacedName, poolFound bool, pairs []PairReconciliation) metav1.Condition {
	condition := func(status metav1.ConditionStatus, reason, message string) metav1.Condition {
		return metav1.Condition{
			Type: kagentv1alpha3.HarnessConditionTypeReady, Status: status, Reason: reason, Message: message, ObservedGeneration: generation,
		}
	}
	if !poolFound {
		return condition(metav1.ConditionFalse, "WorkerPoolNotFound", fmt.Sprintf("WorkerPool %q not found", pool.String()))
	}
	slices.SortFunc(pairs, func(a, b PairReconciliation) int { return strings.Compare(a.ResourceName(), b.ResourceName()) })
	var failed, retrying, pending *metav1.Condition
	for _, state := range pairs {
		if state.Revision == nil {
			continue
		}
		template := state.Pair.AgentTemplate.Namespace + "/" + state.Pair.AgentTemplate.Name
		switch failure := state.Failure; {
		case failure != nil && failure.Condition == kagentv1alpha3.AgentTemplateConditionReady:
			if failed == nil {
				c := condition(metav1.ConditionFalse, failure.Reason, fmt.Sprintf("AgentTemplate %s: %s", template, failure.Message))
				failed = &c
			}
		case failure != nil:
		case state.ObservedActorTemplate.GetStatus().GetGoldenSnapshotStatus().GetGoldenTag() != nil:
			return condition(metav1.ConditionTrue, "Booted", fmt.Sprintf("golden boot of AgentTemplate %s succeeded", template))
		case state.GoldenBootRetry != nil:
			if retrying == nil {
				retry := state.GoldenBootRetry
				c := condition(metav1.ConditionFalse, "ActorTemplateRetrying", fmt.Sprintf("AgentTemplate %s: %s", template, goldenBootFailedMessage(retry.Attempt, retry.Message, "starting it over")))
				retrying = &c
			}
		default:
			if pending == nil {
				c := condition(metav1.ConditionUnknown, "BootPending", fmt.Sprintf("waiting for the golden boot of AgentTemplate %s", template))
				pending = &c
			}
		}
	}
	for _, c := range []*metav1.Condition{failed, retrying, pending} {
		if c != nil {
			return *c
		}
	}
	return condition(metav1.ConditionUnknown, "NoAgentTemplates", "the Harness admits no AgentTemplate yet")
}

func requestedRevision(template *kagentv1alpha3.AgentTemplate, harness string) string {
	raw, _ := json.Marshal(struct {
		UID        types.UID
		Generation int64
		Spec       kagentv1alpha3.AgentTemplateSpec
		Harness    string
	}{template.UID, template.Generation, template.Spec, harness})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
