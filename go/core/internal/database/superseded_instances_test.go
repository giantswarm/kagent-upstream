package database

import (
	"testing"

	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/google/uuid"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestAgentInstancesOnSupersededRevisionsAreListedByID(t *testing.T) {
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
	suspending := readyInstance()
	_, err := client.BeginAgentInstanceOperation(ctx, suspending.GetId(), apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_SUSPEND)
	require.NoError(t, err)

	listed, err := client.ListAgentInstancesOnSupersededRevisions(ctx, "", 10)
	require.NoError(t, err)
	require.Empty(t, listed, "every instance is on its agent's current revision")

	// The agent is re-rendered: revision-2 becomes current.
	current := RuntimeRevision{
		Revision: "revision-2", Namespace: "team-a", AgentTemplateName: "assistant", AgentTemplateUID: "assistant-uid",
		HarnessName: "kagent", HarnessUID: "kagent-uid", SourceSnapshot: []byte("{}"), AgentCard: &a2apb.AgentCard{}, EgressDestinations: []string{},
		ActorTemplateAtespace: "team-a", ActorTemplateName: "revision-2-actor-template", ActorTemplateUID: "revision-2-actor-uid",
	}
	require.NoError(t, client.UpsertAgentTemplateHarnessPair(ctx, AgentTemplateHarnessPair{
		Namespace: "team-a", AgentTemplateName: "assistant", AgentTemplateUID: "assistant-uid",
		HarnessName: "kagent", HarnessUID: "kagent-uid", DesiredRevision: current.Revision,
	}))
	require.NoError(t, client.RecordRuntimeRevision(ctx, current, true))
	moved := readyInstance()

	listed, err = client.ListAgentInstancesOnSupersededRevisions(ctx, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{first.GetId(), second.GetId()}, ids(listed), "READY instances without an operation on the superseded revision, by id; the new instance is on the current one")
	require.Equal(t, "revision-1", listed[0].GetPreparedRevision())

	listed, err = client.ListAgentInstancesOnSupersededRevisions(ctx, "", 1)
	require.NoError(t, err)
	require.Equal(t, []string{first.GetId()}, ids(listed))
	listed, err = client.ListAgentInstancesOnSupersededRevisions(ctx, first.GetId(), 1)
	require.NoError(t, err)
	require.Equal(t, []string{second.GetId()}, ids(listed), "the page after an id")

	_, err = client.RepointAgentInstance(ctx, first.GetId(), "revision-1", current.Revision)
	require.NoError(t, err)
	listed, err = client.ListAgentInstancesOnSupersededRevisions(ctx, "", 10)
	require.NoError(t, err)
	require.Equal(t, []string{second.GetId()}, ids(listed), "a moved instance leaves the listing")
	require.Equal(t, "revision-2", moved.GetPreparedRevision())

	// The pair retired: its instances have no current revision to move to.
	require.NoError(t, client.RetirePairIdentities(ctx, "team-a", "assistant", "kagent", nil))
	listed, err = client.ListAgentInstancesOnSupersededRevisions(ctx, "", 10)
	require.NoError(t, err)
	require.Empty(t, listed)
}

func TestRepointAgentInstanceIsIdempotentAndYieldsToAnOperation(t *testing.T) {
	client, ctx := NewClient(setupTestDB(t)), t.Context()
	agentInstanceFixture(t, client, ctx, "team-a", "revision-1", "assistant", "kagent")
	agentInstanceFixture(t, client, ctx, "team-a", "revision-2", "assistant", "kagent")
	instance, _, err := client.CreateAgentInstance(ctx, newAgentInstanceRequest(uuid.NewString(), "assistant", "kagent", ""), uuid.NewString())
	require.NoError(t, err)
	_, err = markAgentInstanceReady(ctx, client, instance.GetId(), "agent.example")
	require.NoError(t, err)
	require.Equal(t, "revision-2", instance.GetPreparedRevision(), "a new instance takes the current revision")

	moved, err := client.RepointAgentInstance(ctx, instance.GetId(), "revision-2", "revision-1")
	require.NoError(t, err)
	require.Equal(t, "revision-1", moved.GetPreparedRevision())
	again, err := client.RepointAgentInstance(ctx, instance.GetId(), "revision-2", "revision-1")
	require.NoError(t, err, "a repoint another caller recorded is accepted")
	require.Equal(t, "revision-1", again.GetPreparedRevision())
	require.Equal(t, moved.GetUpdatedAt().AsTime(), again.GetUpdatedAt().AsTime(), "and changes nothing")

	_, err = client.BeginAgentInstanceOperation(ctx, instance.GetId(), apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_SUSPEND)
	require.NoError(t, err)
	refused, err := client.RepointAgentInstance(ctx, instance.GetId(), "revision-1", "revision-2")
	require.ErrorIs(t, err, ErrConflict, "a lifecycle operation that claimed the instance wins")
	require.Equal(t, "revision-1", refused.GetPreparedRevision(), "the refusal reports the instance as it is")
	require.Equal(t, apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_SUSPEND, refused.GetOperation())
}
