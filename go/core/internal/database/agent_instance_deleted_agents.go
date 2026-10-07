package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
)

// ListAgentInstancesOfDeletedAgents returns the instances whose agent is gone —
// no active AgentTemplate/Harness pair at the template name and harness name
// their prepared revision was rendered for — that are not already deleted, by id
// after afterID, up to limit. An instance of an agent re-rendered, or deleted
// and created again under the same name, is not listed: a pair at that name is
// still active, so it keeps its pinned revision and the revision sweep moves it.
// Callers authorize access.
func (c *Client) ListAgentInstancesOfDeletedAgents(ctx context.Context, afterID string, limit int) ([]*apiv1alpha1.AgentInstance, error) {
	rows, err := queryMany(ctx, c.db, `
		SELECT i.id, i.user_id, i.prepared_revision, i.state, i.data, i.operation, i.context_id,
		    i.source_checkpoint_id, i.history_id, i.operation_id, i.executor_id
		FROM agent_instance i
		JOIN runtime_revision prepared ON prepared.revision = i.prepared_revision
		WHERE i.state <> 'AGENT_INSTANCE_STATE_DELETED'
		  AND NOT EXISTS (
		      SELECT 1 FROM agent_template_harness_pair p
		      WHERE p.namespace = prepared.namespace
		        AND p.agent_template_name = prepared.agent_template_name
		        AND p.harness_name = prepared.harness_name
		        AND p.retired_at IS NULL)
		  AND ($1 = '' OR i.id > $1::uuid)
		ORDER BY i.id
		LIMIT $2
	`, pgx.RowToStructByName[agentInstanceRow], afterID, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list AgentInstances of deleted agents: %w", err)
	}
	result := make([]*apiv1alpha1.AgentInstance, 0, len(rows))
	for _, row := range rows {
		instance, err := toAgentInstance(row)
		if err != nil {
			return nil, err
		}
		result = append(result, instance)
	}
	return result, nil
}
