package database

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/kagent-dev/kagent/go/core/internal/egress"
)

// An Agent's grants live on its active definition, outside its revisions:
// every revision of the identity reads the current ones, and a retired or
// replaced identity's revisions read none.
func TestRuntimeEgressGrantFollowsTheActiveDefinition(t *testing.T) {
	client := NewClient(setupTestDB(t), "public")
	ctx := t.Context()
	sessionFixture(t, client, ctx, "team-a", "revision", "assistant", "kagent")
	tickets := egress.Access{
		Destinations: []string{"https://tickets.example.com:443"},
		Credentials: []egress.Credential{{Hostname: "tickets.example.com", Header: "authorization", Prefix: "Bearer ",
			URI: egress.CredentialURI(egress.KubernetesSecretAuthority, "team-a", "tickets", "token")}},
	}
	definition := AgentDefinition{Namespace: "team-a", AgentName: "assistant", AgentUID: "assistant-uid", DesiredRevision: "revision", EgressGrant: tickets}

	grant, err := client.GetRuntimeEgressGrant(ctx, "revision")
	require.NoError(t, err)
	require.Empty(t, grant.Destinations, "a definition stored without grants holds none")

	require.NoError(t, client.UpsertAgentDefinition(ctx, definition))
	grant, err = client.GetRuntimeEgressGrant(ctx, "revision")
	require.NoError(t, err)
	require.Equal(t, tickets, grant)
	grant, err = client.GetAgentEgressGrant(ctx, "team-a", "assistant-uid")
	require.NoError(t, err)
	require.Equal(t, tickets, grant)
	revision, err := client.GetRuntimeRevision(ctx, "revision")
	require.NoError(t, err)
	require.Empty(t, revision.EgressDestinations, "grants never enter the revision")

	definition.EgressGrant = egress.Access{}
	require.NoError(t, client.UpsertAgentDefinition(ctx, definition))
	grant, err = client.GetRuntimeEgressGrant(ctx, "revision")
	require.NoError(t, err)
	require.Empty(t, grant.Destinations, "a removed grant is revoked")

	definition.EgressGrant = tickets
	require.NoError(t, client.UpsertAgentDefinition(ctx, definition))
	require.NoError(t, client.UpsertAgentDefinition(ctx, AgentDefinition{Namespace: "team-a", AgentName: "assistant", AgentUID: "replacement-uid", EgressGrant: tickets}))
	grant, err = client.GetRuntimeEgressGrant(ctx, "revision")
	require.NoError(t, err)
	require.Empty(t, grant.Destinations, "a recreated Agent's grants never reach its former UID's actors")
	grant, err = client.GetAgentEgressGrant(ctx, "team-a", "assistant-uid")
	require.NoError(t, err)
	require.Empty(t, grant.Destinations)

	_, err = client.GetRuntimeEgressGrant(ctx, "missing")
	require.ErrorIs(t, err, ErrNotFound)

	invalid := definition
	invalid.EgressGrant = egress.Access{Credentials: tickets.Credentials}
	require.ErrorContains(t, client.UpsertAgentDefinition(ctx, invalid), "grants")
}

// A source's targets are its allocated instances across UIDs, in id order,
// without deleted ones; the sources to recover include a retired identity
// only while it has instances.
func TestRuntimeEgressSourceTargets(t *testing.T) {
	client := NewClient(setupTestDB(t), "public")
	ctx := t.Context()
	sessionFixture(t, client, ctx, "team-a", "revision", "assistant", "kagent")
	sessionFixture(t, client, ctx, "team-a", "other-revision", "other", "kagent")
	source := RuntimeEgressSource{Kind: RuntimeEgressAgent, Namespace: "team-a", Name: "assistant"}

	var ids []string
	for range 3 {
		session, _, err := client.CreateSession(ctx, newSessionRequest(uuid.NewString(), "assistant", "kagent", ""), uuid.NewString())
		require.NoError(t, err)
		_, err = markSessionReady(ctx, client, session.GetId(), "runtime")
		require.NoError(t, err)
		ids = append(ids, session.GetId())
	}
	_, _, err := client.CreateSession(ctx, newSessionRequest(uuid.NewString(), "other", "kagent", ""), uuid.NewString())
	require.NoError(t, err)

	first, err := client.ListRuntimeEgressSourceTargets(ctx, source, "", 2)
	require.NoError(t, err)
	require.Len(t, first, 2)
	rest, err := client.ListRuntimeEgressSourceTargets(ctx, source, first[1].ID, 2)
	require.NoError(t, err)
	var listed []string
	for _, target := range append(first, rest...) {
		require.Equal(t, "revision", target.Revision)
		require.Equal(t, "team-a", target.Atespace)
		require.Equal(t, "revision-actor-template", target.TemplateName)
		listed = append(listed, target.ID)
	}
	require.ElementsMatch(t, ids, listed, "another Agent's sessions are not targets")

	require.NoError(t, deleteSession(ctx, client, ids[0]))
	remaining, err := client.ListRuntimeEgressSourceTargets(ctx, source, "", 10)
	require.NoError(t, err)
	require.Len(t, remaining, 2, "a deleted session has no actor to change")

	_, err = client.ListRuntimeEgressSourceTargets(ctx, RuntimeEgressSource{Kind: "unknown", Namespace: "team-a", Name: "assistant"}, "", 10)
	require.Error(t, err)

	sources, err := client.ListAgentEgressSources(ctx)
	require.NoError(t, err)
	require.Equal(t, []RuntimeEgressSource{{Kind: RuntimeEgressAgent, Namespace: "team-a", Name: "assistant"}, {Kind: RuntimeEgressAgent, Namespace: "team-a", Name: "other"}}, sources)
	require.NoError(t, client.RetireAgentIdentities(ctx, "team-a", "assistant", nil))
	require.NoError(t, client.RetireAgentIdentities(ctx, "team-a", "other", nil))
	sources, err = client.ListAgentEgressSources(ctx)
	require.NoError(t, err)
	require.Contains(t, sources, source, "a retired identity with live instances is recovered")
	remaining, err = client.ListRuntimeEgressSourceTargets(ctx, source, "", 10)
	require.NoError(t, err)
	require.Len(t, remaining, 2, "a retired identity's actors stay targets until their grants are revoked")
}
