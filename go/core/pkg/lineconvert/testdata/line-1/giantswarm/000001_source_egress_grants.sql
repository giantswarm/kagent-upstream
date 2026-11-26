-- +goose Up

-- The EgressPolicy grants an Agent or a SandboxTemplate holds now, outside its
-- runtime revisions: the controller applies them to every running and
-- suspended actor of the source. A definition recorded before this holds none
-- until the controller's next reconcile of its source, which every start does.
ALTER TABLE agent_definition
    ADD COLUMN IF NOT EXISTS grant_destinations TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS grant_credentials JSONB NOT NULL DEFAULT '[]'
        CHECK (jsonb_typeof(grant_credentials) = 'array');
ALTER TABLE sandbox_template_definition
    ADD COLUMN IF NOT EXISTS grant_destinations TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS grant_credentials JSONB NOT NULL DEFAULT '[]'
        CHECK (jsonb_typeof(grant_credentials) = 'array');

-- A source's allocated instances, paged by id across its revisions.
CREATE INDEX IF NOT EXISTS agent_revision_source_idx ON agent_revision (agent_name, revision);
CREATE INDEX IF NOT EXISTS sandbox_revision_source_idx ON sandbox_revision (sandbox_template_name, revision);
CREATE INDEX IF NOT EXISTS runtime_instance_revision_idx ON runtime_instance (prepared_revision, id)
    WHERE state <> 'RUNTIME_STATE_DELETED' AND operation <> 'RUNTIME_OPERATION_DELETE';

-- +goose Down

DROP INDEX IF EXISTS runtime_instance_revision_idx;
DROP INDEX IF EXISTS sandbox_revision_source_idx;
DROP INDEX IF EXISTS agent_revision_source_idx;
ALTER TABLE sandbox_template_definition DROP COLUMN grant_credentials, DROP COLUMN grant_destinations;
ALTER TABLE agent_definition DROP COLUMN grant_credentials, DROP COLUMN grant_destinations;
