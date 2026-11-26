-- +goose Up

-- Where a revision's runtime sends the caller's credential: the hosts of the
-- servers that take it, and whether a use outside any host needs it. NULL for
-- a revision recorded before it was; the controller's next record of the same
-- revision fills it in, and until then a turn hands the runtime the caller's
-- credential as before.
ALTER TABLE runtime_revision ADD COLUMN IF NOT EXISTS caller_credential JSONB;

-- The view expanded r.* when it was created: recreate it to carry the column.
DROP VIEW agent_runtime_revision;
CREATE VIEW agent_runtime_revision AS
    SELECT r.*, a.agent_name, a.agent_uid, a.agent_card
    FROM runtime_revision r JOIN agent_revision a USING (revision) WHERE r.kind = 'agent';

-- +goose Down

DROP VIEW agent_runtime_revision;
ALTER TABLE runtime_revision DROP COLUMN caller_credential;
CREATE VIEW agent_runtime_revision AS
    SELECT r.*, a.agent_name, a.agent_uid, a.agent_card
    FROM runtime_revision r JOIN agent_revision a USING (revision) WHERE r.kind = 'agent';
