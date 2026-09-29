package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// StalledAgentInstanceTask is a working task whose last recorded event is older than a
// cutoff: the turn started, and nothing has been recorded for it since.
type StalledAgentInstanceTask struct {
	InstanceID string
	TaskID     string
	// LastEventAt is when the task's projection was last written.
	LastEventAt time.Time
}

// ListAgentInstanceTasksWorkingBefore returns working tasks whose last event was recorded
// before cutoff, oldest first, up to limit. Tasks of a deleted instance and of an instance
// with a lifecycle operation in progress are left out. Callers authorize access.
func (c *Client) ListAgentInstanceTasksWorkingBefore(ctx context.Context, cutoff time.Time, limit int) ([]StalledAgentInstanceTask, error) {
	tasks, err := queryMany(ctx, c.db, `
		SELECT i.id::text AS instance_id, t.id AS task_id, t.updated_at AS last_event_at
		FROM agent_instance_task t
		JOIN agent_instance i ON i.history_id = t.history_id
		WHERE t.state = 'TASK_STATE_WORKING'
		  AND t.updated_at < $1
		  AND i.state <> 'AGENT_INSTANCE_STATE_DELETED'
		  AND i.operation = 'AGENT_INSTANCE_OPERATION_UNSPECIFIED'
		ORDER BY t.updated_at, i.id
		LIMIT $2
	`, pgx.RowToStructByName[StalledAgentInstanceTask], cutoff, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list stalled AgentInstance tasks: %w", err)
	}
	return tasks, nil
}
