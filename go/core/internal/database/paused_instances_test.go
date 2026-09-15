package database

import (
	"context"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestPausedAgentInstancesAreListedUntilTheirSnapshotIsRecorded(t *testing.T) {
	pool := setupTestDB(t)
	client, ctx := NewClient(pool), t.Context()
	agentInstanceFixture(t, client, ctx, "team-a", "revision", "assistant", "kagent")
	now := time.Now().UTC()
	readyInstance := func() *apiv1alpha1.AgentInstance {
		t.Helper()
		instance, _, err := client.CreateAgentInstance(ctx, newAgentInstanceRequest(uuid.NewString(), "assistant", "kagent", ""), uuid.NewString())
		require.NoError(t, err)
		_, err = markAgentInstanceReady(ctx, client, instance.GetId(), "agent.example")
		require.NoError(t, err)
		return instance
	}
	waitingSince := func(instance *apiv1alpha1.AgentInstance, taskID string, state a2a.TaskState, since time.Time) *a2a.Task {
		t.Helper()
		task := newAgentInstanceTask(taskID, taskID+"-message")
		task.ContextID = instance.GetContextId()
		_, _, err := client.CreateAgentInstanceTask(ctx, instance.GetId(), []byte(taskID), task)
		require.NoError(t, err)
		task.Status = a2a.TaskStatus{State: state, Timestamp: &since, Message: &a2a.Message{
			ID: taskID + "-question", Role: a2a.MessageRoleAgent, TaskID: task.ID, ContextID: task.ContextID,
		}}
		require.NoError(t, client.StoreAgentInstanceTaskEvent(ctx, instance.GetId(), task, task, nil))
		return task
	}

	old := readyInstance()
	waitingSince(old, "old", a2a.TaskStateInputRequired, now.Add(-time.Hour))
	recent := readyInstance()
	waitingSince(recent, "recent", a2a.TaskStateAuthRequired, now.Add(-time.Minute))
	answered := readyInstance()
	waitingSince(answered, "answered", a2a.TaskStateInputRequired, now.Add(-time.Hour))
	_, err := client.ContinueAgentInstanceTask(ctx, answered.GetId(), []byte("answer"), &a2a.Message{
		ID: "answer", Role: a2a.MessageRoleUser, TaskID: "answered", ContextID: answered.GetContextId(),
	})
	require.NoError(t, err)
	suspending := readyInstance()
	waitingSince(suspending, "suspending", a2a.TaskStateInputRequired, now.Add(-time.Hour))
	_, err = client.TransitionAgentInstance(ctx, &apiv1alpha1.AgentInstance{
		Id: suspending.GetId(), State: apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		Operation: apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_SUSPEND,
	}, apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY, apiv1alpha1.AgentInstanceOperation_AGENT_INSTANCE_OPERATION_UNSPECIFIED)
	require.NoError(t, err)

	ids := func(paused []PausedAgentInstance) []string {
		result := make([]string, 0, len(paused))
		for _, candidate := range paused {
			result = append(result, candidate.Instance.GetId()+"/"+candidate.TaskID)
		}
		return result
	}
	paused, err := client.ListAgentInstancesPausedBefore(ctx, now.Add(-10*time.Minute), 10)
	require.NoError(t, err)
	require.Equal(t, []string{old.GetId() + "/old"}, ids(paused), "a reply, a lifecycle operation and a recent pause are not candidates")
	require.WithinDuration(t, now.Add(-time.Hour), paused[0].PausedAt, time.Second)
	require.Equal(t, apiv1alpha1.AgentInstanceState_AGENT_INSTANCE_STATE_READY, paused[0].Instance.GetState())
	require.Equal(t, old.GetContextId(), paused[0].Instance.GetContextId())
	paused, err = client.ListAgentInstancesPausedBefore(ctx, now, 10)
	require.NoError(t, err)
	require.Equal(t, []string{old.GetId() + "/old", recent.GetId() + "/recent"}, ids(paused), "oldest pause first")
	paused, err = client.ListAgentInstancesPausedBefore(ctx, now, 1)
	require.NoError(t, err)
	require.Equal(t, []string{old.GetId() + "/old"}, ids(paused))

	snapshot := &AgentInstanceTaskSnapshot{Atespace: "team-a", URI: "s3://snapshots/paused-1", ContentScope: "FULL"}
	require.ErrorIs(t, client.RecordAgentInstanceTaskSnapshot(ctx, old.GetId(), "old", a2a.TaskStateAuthRequired, snapshot), ErrConflict, "the task waits in another state")
	require.ErrorIs(t, client.RecordAgentInstanceTaskSnapshot(ctx, old.GetId(), "missing", a2a.TaskStateInputRequired, snapshot), ErrNotFound)
	require.ErrorIs(t, client.RecordAgentInstanceTaskSnapshot(ctx, uuid.NewString(), "old", a2a.TaskStateInputRequired, snapshot), ErrNotFound)
	require.ErrorIs(t, client.RecordAgentInstanceTaskSnapshot(ctx, answered.GetId(), "answered", a2a.TaskStateInputRequired, snapshot), ErrConflict, "the task has moved on")
	require.ErrorContains(t, client.RecordAgentInstanceTaskSnapshot(ctx, old.GetId(), "old", a2a.TaskStateInputRequired, nil), "snapshot is required")
	require.NoError(t, client.RecordAgentInstanceTaskSnapshot(ctx, old.GetId(), "old", a2a.TaskStateInputRequired, snapshot))
	require.ErrorIs(t, client.RecordAgentInstanceTaskSnapshot(ctx, old.GetId(), "old", a2a.TaskStateInputRequired, snapshot), ErrConflict, "a boundary is recorded once")

	paused, err = client.ListAgentInstancesPausedBefore(ctx, now, 10)
	require.NoError(t, err)
	require.Equal(t, []string{recent.GetId() + "/recent"}, ids(paused), "a recorded snapshot makes the pause durable")

	task, err := client.GetAgentInstanceTask(ctx, old.GetId(), "old", nil)
	require.NoError(t, err)
	require.Equal(t, a2a.TaskStateInputRequired, task.Status.State, "the task keeps waiting")
	require.NotNil(t, task.Status.Message)
	require.Equal(t, "old-question", task.Status.Message.ID, "the question stays where the reply archives it")
	require.Len(t, task.History, 1)

	instanceRow, err := readAgentInstance(ctx, pool, old.GetId())
	require.NoError(t, err)
	row, err := readAgentInstanceTask(ctx, pool, instanceRow.HistoryID, "old")
	require.NoError(t, err)
	require.NotNil(t, row.SnapshotURI)
	require.Equal(t, snapshot.URI, *row.SnapshotURI)
	require.Equal(t, snapshot.ContentScope, *row.SnapshotContentScope)
	events, err := queryMany(ctx, pool, `
		SELECT sequence, history_id, task_id, data, created_at, message_id, task_position, initial_message_id,
		    request_hash, snapshot_atespace, snapshot_uri, snapshot_content_scope FROM agent_instance_task_event WHERE
		    history_id = $1 ORDER BY sequence
	`, pgx.RowToStructByName[agentInstanceTaskEventRow], instanceRow.HistoryID)
	require.NoError(t, err)
	boundary := events[len(events)-1]
	require.NotNil(t, row.HistorySequence)
	require.Equal(t, boundary.Sequence, *row.HistorySequence, "the boundary is the newest event")
	require.Equal(t, snapshot.URI, *boundary.SnapshotURI)
	rebuilt, err := replayTaskEvents(events, old.GetContextId())
	require.NoError(t, err)
	require.Len(t, rebuilt, 1)
	want, got := &a2apb.Task{}, &a2apb.Task{}
	require.NoError(t, proto.Unmarshal(row.Data, want))
	require.NoError(t, proto.Unmarshal(rebuilt[0].Data, got))
	require.True(t, proto.Equal(want, got), "replay differs from the live task")
	require.Equal(t, row.State, rebuilt[0].State)
	require.Equal(t, *row.SnapshotURI, *rebuilt[0].SnapshotURI)
	require.Equal(t, *row.HistorySequence, *rebuilt[0].HistorySequence)

	continuation, err := client.ContinueAgentInstanceTask(ctx, old.GetId(), []byte("late-answer"), &a2a.Message{
		ID: "late-answer", Role: a2a.MessageRoleUser, TaskID: "old", ContextID: old.GetContextId(),
	})
	require.NoError(t, err, "the answer after the snapshot continues the task")
	require.Equal(t, a2a.TaskStateSubmitted, continuation.Current.Status.State)
	require.Len(t, continuation.Current.History, 3, "the question and the answer joined the history")
}

func TestRecordAgentInstanceTaskSnapshotRequiresASnapshot(t *testing.T) {
	require.ErrorContains(t, (&Client{}).RecordAgentInstanceTaskSnapshot(context.Background(), uuid.NewString(), "task", a2a.TaskStateInputRequired, &AgentInstanceTaskSnapshot{}), "snapshot is required")
}
