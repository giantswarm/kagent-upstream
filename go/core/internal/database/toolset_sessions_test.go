package database

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
)

const toolsetGoImage = "registry.example.com/tools/go@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

// toolsetRevision derives a toolset revision from the Agent's latest one, as
// the session service does after preparing its ActorTemplate.
func toolsetRevision(t *testing.T, client *Client, ctx context.Context, revision string) RuntimeRevision {
	t.Helper()
	base, err := client.GetLatestAgentRevision(ctx, "team-a", "assistant")
	require.NoError(t, err)
	toolset := *base
	toolset.Revision = revision
	toolset.EgressDestinations = append(append([]string(nil), base.EgressDestinations...), "https://proxy.golang.org:443")
	toolset.ActorTemplateName, toolset.ActorTemplateUID = revision+"-actor-template", revision+"-actor-uid"
	return toolset
}

func toolsetSessionRequest(oci string) *apiv1alpha1.Session {
	request := newSessionRequest(uuid.NewString(), "assistant", "kagent", "")
	request.ToolImages = []*apiv1alpha1.SessionToolImage{{Name: "go", Oci: oci, EgressDestinations: []string{"https://proxy.golang.org"}}}
	return request
}

func unreferencedRevisions(t *testing.T, client *Client, ctx context.Context) []string {
	t.Helper()
	artifacts, err := client.ListUnreferencedRuntimeRevisions(ctx)
	require.NoError(t, err)
	revisions := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		revisions = append(revisions, artifact.Revision)
	}
	return revisions
}

func TestGetLatestAgentRevision(t *testing.T) {
	client := NewClient(setupTestDB(t), "public")
	ctx := t.Context()
	_, err := client.GetLatestAgentRevision(ctx, "team-a", "assistant")
	require.ErrorIs(t, err, ErrNotFound)

	sessionFixture(t, client, ctx, "team-a", "base", "assistant", "kagent")
	revision, err := client.GetLatestAgentRevision(ctx, "team-a", "assistant")
	require.NoError(t, err)
	require.Equal(t, "base", revision.Revision)
	require.Equal(t, "assistant-uid", revision.AgentUID)
	require.Equal(t, "base-actor-template", revision.ActorTemplateName)
	require.NotNil(t, revision.AgentCard)

	require.NoError(t, client.RetireAgentIdentities(ctx, "team-a", "assistant", nil))
	_, err = client.GetLatestAgentRevision(ctx, "team-a", "assistant")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestCreateToolsetSessionPinsAndRetainsItsRevision(t *testing.T) {
	client := NewClient(setupTestDB(t), "public")
	ctx := t.Context()
	sessionFixture(t, client, ctx, "team-a", "base", "assistant", "kagent")
	toolset := toolsetRevision(t, client, ctx, "toolset")

	request := toolsetSessionRequest(toolsetGoImage)
	session, created, err := client.CreateToolsetSession(ctx, request, "request", toolset)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "toolset", session.GetPreparedRevision())
	require.Equal(t, apiv1alpha1.RuntimeState_RUNTIME_STATE_CREATING, session.GetState())
	require.True(t, proto.Equal(request.GetToolImages()[0], session.GetToolImages()[0]))

	stored, err := client.GetRuntimeRevision(ctx, "toolset")
	require.NoError(t, err)
	require.Equal(t, toolset.EgressDestinations, stored.EgressDestinations)
	require.Equal(t, "toolset-actor-template", stored.ActorTemplateName)
	require.Equal(t, "assistant", stored.AgentName)

	latest, err := client.GetLatestAgentRevision(ctx, "team-a", "assistant")
	require.NoError(t, err)
	require.Equal(t, "base", latest.Revision, "a toolset revision is never the Agent's latest")
	require.NotContains(t, unreferencedRevisions(t, client, ctx), "toolset")

	// A retry returns the session; the same request ID with other tools is a conflict.
	replayed, created, err := client.CreateToolsetSession(ctx, proto.CloneOf(request), "request", toolset)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, session.GetId(), replayed.GetId())
	other := proto.CloneOf(request)
	other.ToolImages[0].Oci = "registry.example.com/tools/go@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	_, _, err = client.CreateToolsetSession(ctx, other, "request", toolset)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	_, _, err = client.CreateSession(ctx, proto.CloneOf(request), "request")
	require.NoError(t, err, "the same request without a prepared toolset replays too")

	// A second session of the same set shares the revision.
	second, _, err := client.CreateToolsetSession(ctx, toolsetSessionRequest(toolsetGoImage), "second", toolset)
	require.NoError(t, err)
	require.Equal(t, "toolset", second.GetPreparedRevision())

	require.NoError(t, deleteSession(ctx, client, session.GetId()))
	require.NotContains(t, unreferencedRevisions(t, client, ctx), "toolset")
	require.NoError(t, deleteSession(ctx, client, second.GetId()))
	require.Contains(t, unreferencedRevisions(t, client, ctx), "toolset", "cleanup removes it once no session references it")
}

func TestReservedToolsetRevisionIsCollectedAfterItsGrace(t *testing.T) {
	client := NewClient(setupTestDB(t), "public")
	ctx := t.Context()
	sessionFixture(t, client, ctx, "team-a", "base", "assistant", "kagent")
	toolset := toolsetRevision(t, client, ctx, "toolset")
	age := func(revision string) {
		t.Helper()
		_, err := client.db.Exec(ctx, `UPDATE runtime_revision SET updated_at = NOW() - INTERVAL '31 minutes' WHERE revision = $1`, revision)
		require.NoError(t, err)
	}

	// A preparation reserves the revision before its template; cleanup holds it.
	require.NoError(t, client.ReserveToolsetRevision(ctx, toolset))
	reserved, err := client.GetRuntimeRevision(ctx, "toolset")
	require.NoError(t, err)
	require.Equal(t, "toolset-actor-template", reserved.ActorTemplateName)
	require.Empty(t, reserved.ActorTemplateUID, "a reservation is bound to no template yet")
	require.NotContains(t, unreferencedRevisions(t, client, ctx), "toolset")
	claimed, err := client.BeginRuntimeRevisionDeletion(ctx, "toolset")
	require.NoError(t, err)
	require.Nil(t, claimed, "cleanup cannot claim a fresh reservation")

	// A retry within the grace refreshes the reservation and creates the session.
	age("toolset")
	require.NoError(t, client.ReserveToolsetRevision(ctx, toolset))
	require.NotContains(t, unreferencedRevisions(t, client, ctx), "toolset")
	session, _, err := client.CreateToolsetSession(ctx, toolsetSessionRequest(toolsetGoImage), "request", toolset)
	require.NoError(t, err)
	bound, err := client.GetRuntimeRevision(ctx, "toolset")
	require.NoError(t, err)
	require.Equal(t, "toolset-actor-uid", bound.ActorTemplateUID)
	require.NoError(t, client.ReserveToolsetRevision(ctx, toolset))
	bound, err = client.GetRuntimeRevision(ctx, "toolset")
	require.NoError(t, err)
	require.Equal(t, "toolset-actor-uid", bound.ActorTemplateUID, "a later reservation keeps the bound template")
	require.NoError(t, deleteSession(ctx, client, session.GetId()))
	require.Contains(t, unreferencedRevisions(t, client, ctx), "toolset", "a bound revision needs no grace")

	// A preparation abandoned before its session is collected after the grace.
	abandoned := toolsetRevision(t, client, ctx, "abandoned")
	require.NoError(t, client.ReserveToolsetRevision(ctx, abandoned))
	require.NotContains(t, unreferencedRevisions(t, client, ctx), "abandoned")
	age("abandoned")
	require.Contains(t, unreferencedRevisions(t, client, ctx), "abandoned")
	claimed, err = client.BeginRuntimeRevisionDeletion(ctx, "abandoned")
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.Equal(t, "abandoned-actor-template", claimed.ActorTemplateName)
	require.Empty(t, claimed.ActorTemplateUID)
	require.ErrorIs(t, client.ReserveToolsetRevision(ctx, abandoned), ErrObjectDeleting, "a revision being deleted is not reserved again")
	require.NoError(t, client.DeleteRuntimeRevision(ctx, "abandoned", ""))
	_, err = client.GetRuntimeRevision(ctx, "abandoned")
	require.ErrorIs(t, err, ErrNotFound)

	// A later preparation of the same set reserves it afresh.
	require.NoError(t, client.ReserveToolsetRevision(ctx, abandoned))
	require.NotContains(t, unreferencedRevisions(t, client, ctx), "abandoned")

	// Only an active Agent's revision is reserved.
	retired := toolsetRevision(t, client, ctx, "retired")
	require.NoError(t, client.RetireAgentIdentities(ctx, "team-a", "assistant", nil))
	require.ErrorIs(t, client.ReserveToolsetRevision(ctx, retired), ErrNotFound)
}

func TestCreateToolsetSessionRefusesAnotherAgentIdentity(t *testing.T) {
	client := NewClient(setupTestDB(t), "public")
	ctx := t.Context()
	sessionFixture(t, client, ctx, "team-a", "base", "assistant", "kagent")
	sessionFixture(t, client, ctx, "team-a", "other-base", "other", "kagent")

	toolset := toolsetRevision(t, client, ctx, "toolset")
	toolset.AgentName = "other"
	_, _, err := client.CreateToolsetSession(ctx, toolsetSessionRequest(toolsetGoImage), "other-agent", toolset)
	require.ErrorIs(t, err, ErrConflict)

	replaced := toolsetRevision(t, client, ctx, "replaced")
	replaced.AgentUID = "replaced-uid"
	_, _, err = client.CreateToolsetSession(ctx, toolsetSessionRequest(toolsetGoImage), "replaced", replaced)
	require.ErrorIs(t, err, ErrNotFound)

	retired := toolsetRevision(t, client, ctx, "retired")
	require.NoError(t, client.RetireAgentIdentities(ctx, "team-a", "assistant", nil))
	_, _, err = client.CreateToolsetSession(ctx, toolsetSessionRequest(toolsetGoImage), "retired", retired)
	require.ErrorIs(t, err, ErrNotFound)

	for _, revision := range []string{"toolset", "replaced", "retired"} {
		_, err := client.GetRuntimeRevision(ctx, revision)
		require.ErrorIs(t, err, ErrNotFound, "a refused session records no revision")
	}
}
