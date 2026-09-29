package database

import (
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/google/uuid"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestWorkingTasksAreListedOnceTheyRecordedNoEventSinceTheCutoff(t *testing.T) {
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
	lastEventAt := func(instance *apiv1alpha1.AgentInstance, taskID string, state a2a.TaskState, at time.Time) {
		t.Helper()
		task := newAgentInstanceTask(taskID, taskID+"-message")
		task.ContextID = instance.GetContextId()
		_, _, err := client.CreateAgentInstanceTask(ctx, instance.GetId(), []byte(taskID), task)
		require.NoError(t, err)
		task.Status = a2a.TaskStatus{State: state, Timestamp: &at}
		require.NoError(t, client.StoreAgentInstanceTaskEvent(ctx, instance.GetId(), task, task, nil))
		row, err := readAgentInstance(ctx, pool, instance.GetId())
		require.NoError(t, err)
		require.NoError(t, execSQL(ctx, pool, `UPDATE agent_instance_task SET updated_at = $3 WHERE history_id = $1 AND id = $2`,
			row.HistoryID, taskID, at))
	}

	lost := readyInstance()
	lastEventAt(lost, "lost", a2a.TaskStateWorking, now.Add(-13*24*time.Hour))
	quiet := readyInstance()
	lastEventAt(quiet, "quiet", a2a.TaskStateWorking, now.Add(-2*time.Hour))
	recent := readyInstance()
	lastEventAt(recent, "recent", a2a.TaskStateWorking, now.Add(-time.Minute))
	completed := readyInstance()
	lastEventAt(completed, "completed", a2a.TaskStateCompleted, now.Add(-2*time.Hour))
	suspending := readyInstance()
	lastEventAt(suspending, "suspending", a2a.TaskStateWorking, now.Add(-2*time.Hour))
	// A lifecycle operation refuses to start under an active task; one that did
	// (a delete, a recovery) owns the task.
	require.NoError(t, execSQL(ctx, pool, `UPDATE agent_instance SET operation = 'AGENT_INSTANCE_OPERATION_SUSPEND' WHERE id = $1`,
		suspending.GetId()))

	ids := func(stalled []StalledAgentInstanceTask) []string {
		result := make([]string, 0, len(stalled))
		for _, candidate := range stalled {
			result = append(result, candidate.InstanceID+"/"+candidate.TaskID)
		}
		return result
	}
	stalled, err := client.ListAgentInstanceTasksWorkingBefore(ctx, now.Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Equal(t, []string{lost.GetId() + "/lost", quiet.GetId() + "/quiet"}, ids(stalled),
		"oldest first; a recent event, a terminal task and a lifecycle operation are not candidates")
	require.WithinDuration(t, now.Add(-13*24*time.Hour), stalled[0].LastEventAt, time.Second)
	stalled, err = client.ListAgentInstanceTasksWorkingBefore(ctx, now.Add(-time.Hour), 1)
	require.NoError(t, err)
	require.Equal(t, []string{lost.GetId() + "/lost"}, ids(stalled))

	interrupted, err := client.InterruptActiveAgentInstanceTask(ctx, lost.GetId(), "lost")
	require.NoError(t, err)
	require.True(t, interrupted)
	task, err := client.GetAgentInstanceTask(ctx, lost.GetId(), "lost", nil)
	require.NoError(t, err)
	require.Equal(t, a2a.TaskStateFailed, task.Status.State)
	require.NotNil(t, task.Status.Message, "the failure names its reason")
	stalled, err = client.ListAgentInstanceTasksWorkingBefore(ctx, now.Add(-time.Hour), 10)
	require.NoError(t, err)
	require.Equal(t, []string{quiet.GetId() + "/quiet"}, ids(stalled), "a failed task is no longer a candidate")
	_, err = client.GetActiveAgentInstanceTask(ctx, lost.GetId())
	require.ErrorIs(t, err, ErrNotFound, "the instance takes the next message")
}
