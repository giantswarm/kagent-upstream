package database

import (
	"context"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
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

func toolsetSessionRequest(oci string) *apiv1.Session {
	request := newSessionRequest(uuid.NewString(), "assistant", "kagent", "")
	request.ToolImages = []*apiv1.SessionToolImage{{Name: "go", Oci: oci, EgressDestinations: []string{"https://proxy.golang.org"}}}
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

	require.NoError(t, client.RetireAgentIdentities(ctx, "team-a", "assistant"))
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
	require.Equal(t, apiv1.RuntimeState_RUNTIME_STATE_CREATING, session.GetState())
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
	require.NoError(t, client.RetireAgentIdentities(ctx, "team-a", "assistant"))
	_, _, err = client.CreateToolsetSession(ctx, toolsetSessionRequest(toolsetGoImage), "retired", retired)
	require.ErrorIs(t, err, ErrNotFound)

	for _, revision := range []string{"toolset", "replaced", "retired"} {
		_, err := client.GetRuntimeRevision(ctx, revision)
		require.ErrorIs(t, err, ErrNotFound, "a refused session records no revision")
	}
}

func TestForkOfAToolsetSessionKeepsItsToolImages(t *testing.T) {
	client := NewClient(setupTestDB(t), "public")
	ctx := t.Context()
	sessionFixture(t, client, ctx, "team-a", "base", "assistant", "kagent")
	toolset := toolsetRevision(t, client, ctx, "toolset")
	source, _, err := client.CreateToolsetSession(ctx, toolsetSessionRequest(toolsetGoImage), "source", toolset)
	require.NoError(t, err)
	_, err = markSessionReady(ctx, client, source.GetId(), "source.example")
	require.NoError(t, err)

	task := newSessionTask("task-1", "message-1")
	task.ContextID = source.GetContextId()
	task.History[0].ContextID = source.GetContextId()
	task.History[0].TaskID = task.ID
	task.History[0].ReferenceTasks = []a2a.TaskID{task.ID}
	task.Status.Message = &a2a.Message{ID: "message-1", Role: a2a.MessageRoleAgent, TaskID: task.ID, ContextID: source.GetContextId()}
	_, err = client.CreateRuntimeTask(ctx, source.GetId(), taskMutationHash("message-request-1"), task, "")
	require.NoError(t, err)
	task.Status.State = a2a.TaskStateCompleted
	require.NoError(t, saveRuntimeTask(t, client, source.GetId(), task, task,
		&SessionTaskSnapshot{Atespace: "team-a", URI: "s3://snapshots/snapshot-1", ContentScope: "DATA"}))
	checkpoint, _, err := client.ReserveSessionCheckpoint(ctx, &apiv1.Checkpoint{Id: uuid.NewString(), SessionId: source.GetId(), HeadTaskId: "task-1"}, "alice", "checkpoint")
	require.NoError(t, err)
	require.Len(t, checkpoint.GetToolImages(), 1, "the checkpoint carries the source's tool images")
	_, err = client.FinalizeSessionCheckpoint(ctx, checkpoint.GetId(), "tag-uid-1", "s3://tags/checkpoint", "")
	require.NoError(t, err)
	require.NoError(t, deleteSession(ctx, client, source.GetId()))

	fork, created, err := client.ForkSession(ctx, checkpoint.GetId(), "alice", "fork", uuid.NewString())
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "toolset", fork.GetPreparedRevision())
	require.Len(t, fork.GetToolImages(), 1)
	require.Equal(t, toolsetGoImage, fork.GetToolImages()[0].GetOci())
}
