package e2e_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kagent-dev/kagent/go/api/v1alpha3"
	"github.com/kagent-dev/mockllm"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed mocks/invoke_egress_policy.json
var egressPolicyMocks []byte

const ticketRepository = "tickets.git"

// An EgressPolicy the Agent references grants its Sessions a destination they
// could not reach before and binds the policy's Secret to the header the
// runtime sends with a placeholder: the ticket host answers 401 to any other
// Authorization, so only the gateway's injection lets the call through. The
// grant reaches a Session that already runs, without a new revision, and the
// policy's deletion revokes it there.
func TestEgressPolicyGrantsDestinationAndCredential(t *testing.T) {
	t.Parallel()
	const token = "egress-policy-e2e-token"
	target := interactionTarget(t)
	kube := interactionKubeClient(t)

	skills := newGitFixture(t, "")
	skillServer := skills.serveThroughEgress(t)
	tickets := newGitFixtureOf(t, "Bearer "+token, ticketRepository)
	ticketServer := tickets.serveThroughEgress(t)

	var cfg mockllm.Config
	raw := bytes.ReplaceAll(egressPolicyMocks, []byte("TICKET_REPOSITORY_URL"), []byte(ticketServer+"/"+ticketRepository))
	require.NoError(t, json.Unmarshal(raw, &cfg))
	model := startModelRecorder(t, startMockLLMConfig(t, cfg), nil)
	modelConfig := createInteractionModel(t, kube, reachableModelURL(t, model.URL), nil)
	template := &v1alpha3.AgentTemplate{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "egress-policy-", Namespace: "kagent",
			Labels: map[string]string{"kagent.dev/e2e-runtime": goADKHarness.runtimeLabel, "kagent.dev/harness": goADKHarness.name},
		},
		Spec: v1alpha3.AgentTemplateSpec{
			ModelConfig:  &corev1.LocalObjectReference{Name: modelConfig.Name},
			SystemPrompt: "Use the bash tool.",
			// A skill is what gives the kagent Harness its bash tool.
			Skills: []v1alpha3.AgentTemplateSkill{{Name: gitFixtureSkill, Source: v1alpha3.ArtifactSource{
				Git:  &v1alpha3.GitArtifact{URL: skillServer + "/" + gitSkillRepository, Commit: skills.commit},
				Path: "skills/" + gitFixtureSkill,
			}}},
		},
	}
	createAndWaitInteractionTemplateForHarness(t, kube, template, goADKHarness.name)

	// Without a policy the ticket host is no destination of the Session.
	running := newInteractionFixtureForTemplate(t, goADKHarness, target, template.Name)
	_, _, task := running.send(t, "Call the ticket API.")
	require.Contains(t, taskText(task), "TICKET_API_REFUSED")
	revision := agentRevision(t, kube, template.Name)
	require.Empty(t, tickets.received(ticketRepository), "a Session without the policy reached the ticket host")

	secret := createCredentialSecret(t, kube, token)
	policy := &v1alpha3.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "ticket-api-", Namespace: "kagent"},
		Spec: v1alpha3.EgressPolicySpec{Destinations: []v1alpha3.EgressDestination{{
			Origin: ticketServer,
			Authentication: &v1alpha3.EgressAuthentication{
				Header: "Authorization", Prefix: new("Bearer "),
				SecretKeyRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: secret.Name}, Key: "token"},
			},
		}}},
	}
	createEgressPolicy(t, kube, policy)
	setEgressPolicyRefs(t, kube, template.Name, policy.Name)
	requireEgressPoliciesApplied(t, kube, template.Name, metav1.ConditionTrue, "")
	requireAgentReadiness(t, kube, template.Name, metav1.ConditionTrue, "")
	require.Equal(t, revision, agentRevision(t, kube, template.Name), "a grant prepares no new revision")

	// The running Session receives the grant; a new one starts with it.
	sendUntil(t, running, "Call the ticket API.", "TICKET_API_AUTHORIZED")
	_, _, task = newInteractionFixtureForTemplate(t, goADKHarness, target, template.Name).send(t, "Call the ticket API.")
	require.Contains(t, taskText(task), "TICKET_API_AUTHORIZED")
	requireEgressCredential(t, tickets.received(ticketRepository), "Authorization", "Bearer "+token, everyRoute)

	// Deleting the policy revokes the grant on the running Session; the Agent
	// stays Ready.
	require.NoError(t, kube.Delete(t.Context(), policy))
	requireEgressPoliciesApplied(t, kube, template.Name, metav1.ConditionFalse, "not found")
	requireAgentReadiness(t, kube, template.Name, metav1.ConditionTrue, "")
	sendUntil(t, running, "Call the ticket API.", "TICKET_API_REFUSED")
}

// A reference resolves in the Agent's namespace only: a policy of that name in
// another namespace grants nothing until the namespace has its own, and never
// holds the Agent's readiness.
func TestEgressPolicyResolvesInTheAgentsNamespace(t *testing.T) {
	t.Parallel()
	interactionTarget(t)
	kube := interactionKubeClient(t)
	name := createInteractionTemplate(t, goADKHarness, startInteractionMock(t))

	other := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "egress-policy-other-"}}
	require.NoError(t, kube.Create(t.Context(), other))
	t.Cleanup(func() {
		if err := kube.Delete(context.Background(), other); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete namespace %s: %v", other.Name, err)
		}
	})
	elsewhere := &v1alpha3.EgressPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "registries-" + other.Name, Namespace: other.Name},
		Spec:       v1alpha3.EgressPolicySpec{Destinations: []v1alpha3.EgressDestination{{Origin: "https://registry.npmjs.org"}}},
	}
	createEgressPolicy(t, kube, elsewhere)

	setEgressPolicyRefs(t, kube, name, elsewhere.Name)
	requireEgressPoliciesApplied(t, kube, name, metav1.ConditionFalse, "kagent/"+elsewhere.Name+": EgressPolicy not found")
	requireAgentReadiness(t, kube, name, metav1.ConditionTrue, "")

	local := elsewhere.DeepCopy()
	local.ObjectMeta = metav1.ObjectMeta{Name: elsewhere.Name, Namespace: "kagent"}
	createEgressPolicy(t, kube, local)
	requireEgressPoliciesApplied(t, kube, name, metav1.ConditionTrue, "")
}

func createEgressPolicy(t *testing.T, kube ctrlclient.Client, policy *v1alpha3.EgressPolicy) {
	t.Helper()
	require.NoError(t, kube.Create(t.Context(), policy))
	t.Cleanup(func() {
		if err := kube.Delete(context.Background(), policy); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete EgressPolicy %s/%s: %v", policy.Namespace, policy.Name, err)
		}
	})
}

func setEgressPolicyRefs(t *testing.T, kube ctrlclient.Client, agentName string, policies ...string) {
	t.Helper()
	agent := &v1alpha3.Agent{}
	require.NoError(t, kube.Get(t.Context(), ctrlclient.ObjectKey{Namespace: "kagent", Name: agentName}, agent))
	before := agent.DeepCopy()
	agent.Spec.EgressPolicyRefs = nil
	for _, policy := range policies {
		agent.Spec.EgressPolicyRefs = append(agent.Spec.EgressPolicyRefs, corev1.LocalObjectReference{Name: policy})
	}
	require.NoError(t, kube.Patch(t.Context(), agent, ctrlclient.MergeFrom(before)))
}

// requireAgentReadiness waits until the Agent's current generation is Ready
// with status, and for a false one until a condition of that generation names
// message.
func requireAgentReadiness(t *testing.T, kube ctrlclient.Client, agentName string, status metav1.ConditionStatus, message string) {
	t.Helper()
	agent := &v1alpha3.Agent{}
	err := wait.PollUntilContextTimeout(t.Context(), time.Second, agentReadyTimeout, true, func(ctx context.Context) (bool, error) {
		if err := kube.Get(ctx, ctrlclient.ObjectKey{Namespace: "kagent", Name: agentName}, agent); err != nil {
			return false, err
		}
		ready, named := false, message == ""
		for _, condition := range agent.Status.Conditions {
			if condition.ObservedGeneration != agent.Generation {
				continue
			}
			if condition.Type == v1alpha3.AgentConditionReady && condition.Status == status {
				ready = true
			}
			if message != "" && strings.Contains(condition.Message, message) {
				named = true
			}
		}
		return ready && named, nil
	})
	require.NoError(t, err, "Agent %s: want Ready=%s naming %q; last conditions: %+v", agentName, status, message, agent.Status.Conditions)
}

// requireEgressPoliciesApplied waits until the Agent's current generation
// reports EgressPoliciesApplied with status and a message naming message.
func requireEgressPoliciesApplied(t *testing.T, kube ctrlclient.Client, agentName string, status metav1.ConditionStatus, message string) {
	t.Helper()
	agent := &v1alpha3.Agent{}
	err := wait.PollUntilContextTimeout(t.Context(), time.Second, agentReadyTimeout, true, func(ctx context.Context) (bool, error) {
		if err := kube.Get(ctx, ctrlclient.ObjectKey{Namespace: "kagent", Name: agentName}, agent); err != nil {
			return false, err
		}
		condition := apimeta.FindStatusCondition(agent.Status.Conditions, v1alpha3.AgentConditionEgressPoliciesApplied)
		return condition != nil && condition.ObservedGeneration == agent.Generation && condition.Status == status &&
			strings.Contains(condition.Message, message), nil
	})
	require.NoError(t, err, "Agent %s: want EgressPoliciesApplied=%s naming %q; last conditions: %+v", agentName, status, message, agent.Status.Conditions)
}

// agentRevision is the runtime revision the Agent's status reports.
func agentRevision(t *testing.T, kube ctrlclient.Client, agentName string) string {
	t.Helper()
	agent := &v1alpha3.Agent{}
	require.NoError(t, kube.Get(t.Context(), ctrlclient.ObjectKey{Namespace: "kagent", Name: agentName}, agent))
	return agent.Status.LatestSuccessfulRevision
}

// sendUntil sends text on the fixture's Session until a turn answers want:
// an EgressPolicy change reaches a running Actor asynchronously.
func sendUntil(t *testing.T, fixture *interactionFixture, text, want string) {
	t.Helper()
	var last string
	err := wait.PollUntilContextTimeout(t.Context(), 2*time.Second, 2*time.Minute, true, func(context.Context) (bool, error) {
		_, _, task := fixture.send(t, text)
		last = taskText(task)
		return strings.Contains(last, want), nil
	})
	require.NoError(t, err, "Session %s: want a turn answering %q; last answered %q", fixture.sessionID, want, last)
}
