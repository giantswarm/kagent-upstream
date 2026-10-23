package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kagent-dev/kagent/go/core/internal/egress"
)

// canonicalStoredGrants validates and orders a source's grants for NOT NULL storage.
func canonicalStoredGrants(grants egress.Access) (egress.Access, error) {
	canonical, err := egress.CanonicalAccess(grants.Destinations, grants.Credentials)
	if err != nil {
		return egress.Access{}, err
	}
	if canonical.Destinations == nil {
		canonical.Destinations = []string{}
	}
	if canonical.Credentials == nil {
		canonical.Credentials = []egress.Credential{}
	}
	return canonical, nil
}

// decodeStoredGrants reads absent grants as none and refuses malformed ones.
func decodeStoredGrants(grants egress.Access, err error) (egress.Access, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return egress.Access{}, nil
	}
	if err != nil {
		return egress.Access{}, err
	}
	canonical, err := egress.CanonicalAccess(grants.Destinations, grants.Credentials)
	if err != nil {
		return egress.Access{}, fmt.Errorf("decode source egress grants: %w", err)
	}
	return canonical, nil
}

// GetRuntimeEgressGrant returns the grants the active source identity of a
// revision holds now: none once the Agent or SandboxTemplate was deleted or
// replaced by another UID. A missing or deleting revision is ErrNotFound.
func (c *Client) GetRuntimeEgressGrant(ctx context.Context, revision string) (egress.Access, error) {
	grants, err := queryOne(ctx, c.db, `
  SELECT COALESCE(ad.grant_destinations, sd.grant_destinations, '{}') AS destinations,
    COALESCE(ad.grant_credentials, sd.grant_credentials, '[]') AS credentials
  FROM runtime_revision r
  LEFT JOIN agent_revision a ON r.kind = 'agent' AND a.revision = r.revision
  LEFT JOIN sandbox_revision s ON r.kind = 'sandbox' AND s.revision = r.revision
  LEFT JOIN agent_definition ad ON ad.namespace = r.namespace AND ad.agent_uid = a.agent_uid AND ad.retired_at IS NULL
  LEFT JOIN sandbox_template_definition sd ON sd.namespace = r.namespace AND sd.sandbox_template_uid = s.sandbox_template_uid AND sd.retired_at IS NULL
  WHERE r.revision = $1 AND r.deleted_at IS NULL
    AND (a.revision IS NOT NULL OR s.revision IS NOT NULL)
 `, pgx.RowToStructByName[egress.Access], revision)
	if err != nil {
		return egress.Access{}, fmt.Errorf("get runtime egress grant %s: %w", revision, notFoundOr(err))
	}
	return decodeStoredGrants(grants, nil)
}

// GetAgentEgressGrant returns the grants an active Agent identity last
// admitted; none for a missing or retired one.
func (c *Client) GetAgentEgressGrant(ctx context.Context, namespace, uid string) (egress.Access, error) {
	return decodeStoredGrants(queryOne(ctx, c.db, `
		SELECT grant_destinations AS destinations, grant_credentials AS credentials
		FROM agent_definition WHERE namespace = $1 AND agent_uid = $2 AND retired_at IS NULL
	`, pgx.RowToStructByName[egress.Access], namespace, uid))
}

// GetSandboxEgressGrant returns the grants an active SandboxTemplate identity
// last admitted; none for a missing or retired one.
func (c *Client) GetSandboxEgressGrant(ctx context.Context, namespace, uid string) (egress.Access, error) {
	return decodeStoredGrants(queryOne(ctx, c.db, `
		SELECT grant_destinations AS destinations, grant_credentials AS credentials
		FROM sandbox_template_definition WHERE namespace = $1 AND sandbox_template_uid = $2 AND retired_at IS NULL
	`, pgx.RowToStructByName[egress.Access], namespace, uid))
}

// RuntimeEgressSourceKind names the Kubernetes kind that owns runtime instances.
type RuntimeEgressSourceKind string

const (
	RuntimeEgressAgent   RuntimeEgressSourceKind = runtimeKindAgent
	RuntimeEgressSandbox RuntimeEgressSourceKind = runtimeKindSandbox
)

// RuntimeEgressSource is an Agent or a SandboxTemplate by name: its instances
// include those of former UIDs, whose grants a recreation revokes.
type RuntimeEgressSource struct {
	Kind      RuntimeEgressSourceKind
	Namespace string
	Name      string
}

// RuntimeEgressTarget is an allocated instance of a source, suspended ones included.
type RuntimeEgressTarget struct {
	ID           string
	Revision     string
	Atespace     string
	TemplateName string
	// ActorUID is the Actor a Session recorded; empty for a sandbox.
	ActorUID string
}

// ListRuntimeEgressSourceTargets pages a source's allocated instances across
// its revisions and UIDs in id order: every instance but the deleted, the
// deleting and a creation no executor has issued yet, which applies the
// current grants itself. A missing source yields an empty page.
func (c *Client) ListRuntimeEgressSourceTargets(ctx context.Context, source RuntimeEgressSource, afterID string, limit int) ([]RuntimeEgressTarget, error) {
	if source.Namespace == "" || source.Name == "" {
		return nil, fmt.Errorf("runtime egress source requires a namespace and a name")
	}
	switch source.Kind {
	case RuntimeEgressAgent:
		return queryMany(ctx, c.db, `
  SELECT i.id::text AS id, i.prepared_revision AS revision,
    r.actor_template_atespace AS atespace, r.actor_template_name AS template_name,
    COALESCE(s.actor_uid, '') AS actor_uid
  FROM runtime_instance i
  JOIN runtime_revision r ON r.revision = i.prepared_revision
  JOIN agent_revision a ON a.revision = r.revision
  JOIN session s ON s.id = i.id
  WHERE i.kind = 'agent' AND r.kind = 'agent' AND r.namespace = $1 AND a.agent_name = $2
    AND (i.operation <> 'RUNTIME_OPERATION_CREATE' OR i.executor_id IS NOT NULL)
    AND i.state <> 'RUNTIME_STATE_DELETED' AND i.operation <> 'RUNTIME_OPERATION_DELETE'
    AND i.id > COALESCE(NULLIF($3, '')::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  ORDER BY i.id LIMIT $4
 `, pgx.RowToStructByName[RuntimeEgressTarget], source.Namespace, source.Name, afterID, limit)
	case RuntimeEgressSandbox:
		return queryMany(ctx, c.db, `
  SELECT i.id::text AS id, i.prepared_revision AS revision,
    r.actor_template_atespace AS atespace, r.actor_template_name AS template_name,
    '' AS actor_uid
  FROM runtime_instance i
  JOIN runtime_revision r ON r.revision = i.prepared_revision
  JOIN sandbox_revision s ON s.revision = r.revision
  WHERE i.kind = 'sandbox' AND r.kind = 'sandbox' AND r.namespace = $1 AND s.sandbox_template_name = $2
    AND (i.operation <> 'RUNTIME_OPERATION_CREATE' OR i.executor_id IS NOT NULL)
    AND i.state <> 'RUNTIME_STATE_DELETED' AND i.operation <> 'RUNTIME_OPERATION_DELETE'
    AND i.id > COALESCE(NULLIF($3, '')::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  ORDER BY i.id LIMIT $4
 `, pgx.RowToStructByName[RuntimeEgressTarget], source.Namespace, source.Name, afterID, limit)
	default:
		return nil, fmt.Errorf("unknown runtime egress source kind %q", source.Kind)
	}
}

// ListAgentEgressSources names the active Agent definitions and the retired
// identities that still have instances: a controller stopped between an
// Agent's retirement and the revocation of its actors' grants finishes it at
// its next start.
func (c *Client) ListAgentEgressSources(ctx context.Context) ([]RuntimeEgressSource, error) {
	return queryMany(ctx, c.db, `
  SELECT DISTINCT 'agent' AS kind, d.namespace, d.agent_name AS name
  FROM agent_definition d
  WHERE d.retired_at IS NULL OR EXISTS (
    SELECT 1 FROM agent_revision a
    JOIN runtime_revision r ON r.revision = a.revision
    JOIN runtime_instance i ON i.prepared_revision = r.revision
    WHERE r.namespace = d.namespace AND a.agent_uid = d.agent_uid
      AND i.state <> 'RUNTIME_STATE_DELETED'
  )
  ORDER BY namespace, name
 `, pgx.RowToStructByName[RuntimeEgressSource])
}
