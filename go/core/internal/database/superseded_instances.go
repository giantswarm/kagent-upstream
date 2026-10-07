package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	apiv1alpha1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1alpha1"
)

// ListAgentInstancesOnSupersededRevisions returns READY instances without a
// lifecycle operation whose prepared revision is no longer their agent's
// current one, by id after afterID, up to limit. The current revision is the
// latest successful revision of the pair the prepared revision was rendered
// for; an instance of a retired pair, or of an agent without a current
// revision, has nowhere to move and is left out, as is one whose current
// revision lives in another atespace. Callers authorize access.
func (c *Client) ListAgentInstancesOnSupersededRevisions(ctx context.Context, afterID string, limit int) ([]*apiv1alpha1.AgentInstance, error) {
	rows, err := queryMany(ctx, c.db, `
		SELECT i.id, i.user_id, i.prepared_revision, i.state, i.data, i.operation, i.context_id,
		    i.source_checkpoint_id, i.history_id, i.operation_id, i.executor_id
		FROM agent_instance i
		JOIN runtime_revision prepared ON prepared.revision = i.prepared_revision
		JOIN agent_template_harness_pair p
		  ON p.namespace = prepared.namespace
		 AND p.agent_template_uid = prepared.agent_template_uid
		 AND p.harness_uid = prepared.harness_uid
		 AND p.retired_at IS NULL
		JOIN runtime_revision current ON current.revision = p.latest_successful_revision AND current.deleted_at IS NULL
		WHERE i.state = 'AGENT_INSTANCE_STATE_READY'
		  AND i.operation = 'AGENT_INSTANCE_OPERATION_UNSPECIFIED'
		  AND current.revision <> prepared.revision
		  AND current.actor_template_atespace = prepared.actor_template_atespace
		  AND ($1 = '' OR i.id > $1::uuid)
		ORDER BY i.id
		LIMIT $2
	`, pgx.RowToStructByName[agentInstanceRow], afterID, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list AgentInstances on superseded revisions: %w", err)
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
