package database

import (
	"testing"

	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/google/uuid"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestAgentInstancesOfDeletedAgentsAreListedByID(t *testing.T) {
	client, ctx := NewClient(setupTestDB(t)), t.Context()
	agentInstanceFixture(t, client, ctx, "team-a", "revision-1", "assistant", "kagent")
	readyInstance := func() *apiv1alpha1.AgentInstance {
		t.Helper()
		instance, _, err := client.CreateAgentInstance(ctx, newAgentInstanceRequest(uuid.NewString(), "assistant", "kagent", ""), uuid.NewString())
		require.NoError(t, err)
		_, err = markAgentInstanceReady(ctx, client, instance.GetId(), "agent.example")
		require.NoError(t, err)
		return instance
	}
	ids := func(instances []*apiv1alpha1.AgentInstance) []string {
		result := make([]string, 0, len(instances))
		for _, instance := range instances {
			result = append(result, instance.GetId())
		}
		return result
	}
	first, second := readyInstance(), readyInstance()
	if first.GetId() > second.GetId() {
		first, second = second, first
	}

	listed, err := client.ListAgentInstancesOfDeletedAgents(ctx, "", 10)
	require.NoError(t, err)
	require.Empty(t, listed, "every instance's agent is active")

	// The AgentTemplate is deleted: its pair is retired, its instances have no
	// active pair to keep them.
	require.NoError(t, client.RetirePairIdentities(ctx, "team-a", "assistant", "kagent", nil))
	listed, err = client.ListAgentInstancesOfDeletedAgents(ctx, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{first.GetId(), second.GetId()}, ids(listed), "both READY instances of the deleted agent, by id")

	paged, err := client.ListAgentInstancesOfDeletedAgents(ctx, "", 1)
	require.NoError(t, err)
	require.Equal(t, []string{first.GetId()}, ids(paged))
	paged, err = client.ListAgentInstancesOfDeletedAgents(ctx, first.GetId(), 10)
	require.NoError(t, err)
	require.Equal(t, []string{second.GetId()}, ids(paged))

	// The agent is created again under the same name: a new pair identity is
	// active, so its old instances keep their pinned revision and are left alone.
	current := RuntimeRevision{
		Revision: "revision-2", Namespace: "team-a", AgentTemplateName: "assistant", AgentTemplateUID: "assistant-uid-2",
		HarnessName: "kagent", HarnessUID: "kagent-uid", SourceSnapshot: []byte("{}"), AgentCard: &a2apb.AgentCard{}, EgressDestinations: []string{},
		ActorTemplateAtespace: "team-a", ActorTemplateName: "revision-2-actor-template", ActorTemplateUID: "revision-2-actor-uid",
	}
	require.NoError(t, client.UpsertAgentTemplateHarnessPair(ctx, AgentTemplateHarnessPair{
		Namespace: "team-a", AgentTemplateName: "assistant", AgentTemplateUID: "assistant-uid-2",
		HarnessName: "kagent", HarnessUID: "kagent-uid", DesiredRevision: current.Revision,
	}))
	require.NoError(t, client.RecordRuntimeRevision(ctx, current, true))
	listed, err = client.ListAgentInstancesOfDeletedAgents(ctx, "", 10)
	require.NoError(t, err)
	require.Empty(t, listed, "a pair at the name is active again: the replaced agent's instances stay")
}

// A deleted instance is never relisted, so a completed deletion is not retried.
func TestAgentInstancesOfDeletedAgentsExcludeDeleted(t *testing.T) {
	client, ctx := NewClient(setupTestDB(t)), t.Context()
	agentInstanceFixture(t, client, ctx, "team-a", "revision-1", "assistant", "kagent")
	instance, _, err := client.CreateAgentInstance(ctx, newAgentInstanceRequest(uuid.NewString(), "assistant", "kagent", ""), uuid.NewString())
	require.NoError(t, err)
	_, err = markAgentInstanceReady(ctx, client, instance.GetId(), "agent.example")
	require.NoError(t, err)
	require.NoError(t, client.RetirePairIdentities(ctx, "team-a", "assistant", "kagent", nil))

	listed, err := client.ListAgentInstancesOfDeletedAgents(ctx, "", 10)
	require.NoError(t, err)
	require.Len(t, listed, 1)

	require.NoError(t, client.DeleteAgentInstance(ctx, instance.GetId()))
	listed, err = client.ListAgentInstancesOfDeletedAgents(ctx, "", 10)
	require.NoError(t, err)
	require.Empty(t, listed, "a tombstoned instance is not relisted")
}
