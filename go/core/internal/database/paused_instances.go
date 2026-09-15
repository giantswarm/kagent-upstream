package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/jackc/pgx/v5"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
	"google.golang.org/protobuf/proto"
)

// PausedAgentInstance is a READY instance whose latest task waits for input and whose
// turn boundary has no external snapshot: its runtime was paused on its worker and
// nothing durable holds it yet.
type PausedAgentInstance struct {
	Instance *apiv1alpha1.AgentInstance
	TaskID   string
	// PausedAt is when the task entered its waiting state.
	PausedAt time.Time
}

type pausedAgentInstanceRow struct {
	agentInstanceRow
	TaskID   string
	PausedAt time.Time
}

// ListAgentInstancesPausedBefore returns instances whose runtime has waited for input since
// before cutoff, oldest first, up to limit. An instance with a lifecycle operation in
// progress is left to it. Callers authorize access.
func (c *Client) ListAgentInstancesPausedBefore(ctx context.Context, cutoff time.Time, limit int) ([]PausedAgentInstance, error) {
	rows, err := queryMany(ctx, c.db, `
		SELECT i.id, i.user_id, i.prepared_revision, i.state, i.data, i.operation, i.context_id,
		    i.source_checkpoint_id, i.history_id, i.operation_id, i.executor_id, t.id AS task_id,
		    COALESCE(t.status_timestamp, t.updated_at) AS paused_at
		FROM agent_instance i
		JOIN LATERAL (
		    SELECT id, state, status_timestamp, updated_at, snapshot_uri FROM agent_instance_task
		    WHERE history_id = i.history_id ORDER BY position DESC LIMIT 1
		) t ON TRUE
		WHERE i.state = 'AGENT_INSTANCE_STATE_READY'
		  AND i.operation = 'AGENT_INSTANCE_OPERATION_UNSPECIFIED'
		  AND t.state IN ('TASK_STATE_INPUT_REQUIRED', 'TASK_STATE_AUTH_REQUIRED')
		  AND t.snapshot_uri IS NULL
		  AND COALESCE(t.status_timestamp, t.updated_at) < $1
		ORDER BY paused_at, i.id
		LIMIT $2
	`, pgx.RowToStructByName[pausedAgentInstanceRow], cutoff, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list paused AgentInstances: %w", err)
	}
	result := make([]PausedAgentInstance, 0, len(rows))
	for _, row := range rows {
		instance, err := toAgentInstance(row.agentInstanceRow)
		if err != nil {
			return nil, err
		}
		result = append(result, PausedAgentInstance{Instance: instance, TaskID: row.TaskID, PausedAt: row.PausedAt})
	}
	return result, nil
}

// RecordAgentInstanceTaskSnapshot records the external snapshot of a task that still waits
// in state and has no boundary yet, the way a quiescent turn's is recorded: as a replayable
// boundary event and on the task. It returns ErrConflict when the task has moved on, already
// has a boundary, or a checkpoint of the instance is being created — the snapshot then
// describes a runtime state the next turn will not restore. Callers authorize access.
func (c *Client) RecordAgentInstanceTaskSnapshot(ctx context.Context, instanceID, taskID string, state a2a.TaskState, snapshot *AgentInstanceTaskSnapshot) error {
	if snapshot == nil || snapshot.URI == "" {
		return fmt.Errorf("snapshot is required")
	}
	err := c.withTx(ctx, func(tx pgx.Tx) error {
		instance, err := lockAgentInstance(ctx, tx, instanceID)
		if err != nil {
			return notFoundOr(err)
		}
		if err := requireNoCheckpointCreating(ctx, tx, instance.ID); err != nil {
			return err
		}
		row, err := queryOne(ctx, tx, `
			SELECT history_id, id, state, status_timestamp, data, created_at, updated_at, initial_message_id,
			    request_hash, snapshot_atespace, snapshot_uri, snapshot_content_scope, history_sequence, position FROM
			    agent_instance_task WHERE history_id = $1 AND id = $2 FOR UPDATE
		`, pgx.RowToStructByName[agentInstanceTaskRow], instance.HistoryID, taskID)
		if err != nil {
			return notFoundOr(err)
		}
		if row.State != string(state) || row.SnapshotURI != nil {
			return fmt.Errorf("AgentInstance task %s is not waiting in %s without a snapshot: %w", taskID, state, ErrConflict)
		}
		stored := &a2apb.Task{}
		if err := proto.Unmarshal(row.Data, stored); err != nil {
			return fmt.Errorf("decode stored task: %w", err)
		}
		data, err := proto.Marshal(&a2apb.StreamResponse{Payload: &a2apb.StreamResponse_Task{Task: stored}})
		if err != nil {
			return err
		}
		sequence, err := insertTaskEvent(ctx, tx, taskEventWrite{
			HistoryID: instance.HistoryID, TaskID: &row.ID, Data: data,
			SnapshotAtespace: &snapshot.Atespace, SnapshotURI: &snapshot.URI, SnapshotContentScope: &snapshot.ContentScope,
		})
		if err != nil {
			return fmt.Errorf("store AgentInstance task boundary: %w", err)
		}
		return recordTaskSnapshotBoundary(ctx, tx, instance.HistoryID, row.ID, sequence, snapshot)
	})
	if errors.Is(err, ErrNotFound) || errors.Is(err, ErrConflict) {
		return err
	}
	if err != nil {
		return fmt.Errorf("record AgentInstance task snapshot: %w", err)
	}
	return nil
}
