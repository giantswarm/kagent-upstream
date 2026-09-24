-- +goose Up
-- The 000001 migration is edited in place upstream: a database migrated
-- before runtime_revision.credentials and the agent_instance lifecycle
-- columns exist records version 1 and never receives them. This migration
-- brings such a database to the shape 000001 creates today; on a database
-- 000001 created in its current shape every statement is a no-op.

ALTER TABLE runtime_revision
    ADD COLUMN IF NOT EXISTS credentials JSONB NOT NULL DEFAULT '[]';
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'runtime_revision'::regclass
                     AND pg_get_constraintdef(oid) LIKE '%jsonb_typeof(credentials)%') THEN
        ALTER TABLE runtime_revision
            ADD CONSTRAINT runtime_revision_credentials_check CHECK (jsonb_typeof(credentials) = 'array');
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE agent_instance_checkpoint DROP COLUMN IF EXISTS source_name;

-- +goose StatementBegin
DO $$
DECLARE c TEXT;
BEGIN
    SELECT conname INTO c FROM pg_constraint
     WHERE conrelid = 'agent_instance'::regclass AND contype = 'f'
       AND pg_get_constraintdef(oid) LIKE 'FOREIGN KEY (source_checkpoint_id)%';
    IF c IS NOT NULL THEN
        EXECUTE format('ALTER TABLE agent_instance DROP CONSTRAINT %I', c);
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE agent_instance ADD COLUMN IF NOT EXISTS operation_id UUID;
ALTER TABLE agent_instance ADD COLUMN IF NOT EXISTS executor_id UUID;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_name = 'agent_instance' AND column_name = 'pinned_checkpoint_id') THEN
        ALTER TABLE agent_instance
            ADD COLUMN pinned_checkpoint_id UUID GENERATED ALWAYS AS (
                CASE WHEN state <> 'AGENT_INSTANCE_STATE_DELETED' THEN source_checkpoint_id END
            ) STORED REFERENCES agent_instance_checkpoint(id) ON DELETE RESTRICT;
    END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE c TEXT;
BEGIN
    SELECT conname INTO c FROM pg_constraint
     WHERE conrelid = 'agent_instance'::regclass AND contype = 'c'
       AND pg_get_constraintdef(oid) LIKE '%AGENT_INSTANCE_STATE_CREATING%'
       AND pg_get_constraintdef(oid) NOT LIKE '%AGENT_INSTANCE_STATE_DELETED%';
    IF c IS NOT NULL THEN
        EXECUTE format('ALTER TABLE agent_instance DROP CONSTRAINT %I', c);
        ALTER TABLE agent_instance ADD CONSTRAINT agent_instance_state_check CHECK (state IN (
            'AGENT_INSTANCE_STATE_CREATING', 'AGENT_INSTANCE_STATE_READY',
            'AGENT_INSTANCE_STATE_SUSPENDED', 'AGENT_INSTANCE_STATE_FAILED', 'AGENT_INSTANCE_STATE_DELETED'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'agent_instance'::regclass
                   AND pg_get_constraintdef(oid) LIKE '%executor_id IS NULL%') THEN
        ALTER TABLE agent_instance ADD CONSTRAINT agent_instance_executor_check CHECK (executor_id IS NULL OR (operation_id IS NOT NULL
            AND operation <> 'AGENT_INSTANCE_OPERATION_UNSPECIFIED'
            AND state <> 'AGENT_INSTANCE_STATE_DELETED'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'agent_instance'::regclass
                   AND pg_get_constraintdef(oid) LIKE '%prepared_revision IS NULL%') THEN
        ALTER TABLE agent_instance ADD CONSTRAINT agent_instance_deleted_check CHECK (state <> 'AGENT_INSTANCE_STATE_DELETED' OR
            (prepared_revision IS NULL AND operation = 'AGENT_INSTANCE_OPERATION_UNSPECIFIED'));
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX IF EXISTS agent_instance_user_id_id_idx;
CREATE INDEX agent_instance_user_id_id_idx
    ON agent_instance (user_id, id) WHERE state <> 'AGENT_INSTANCE_STATE_DELETED';

-- +goose Down
-- The shape 000001 created before these columns existed.
DROP INDEX IF EXISTS agent_instance_user_id_id_idx;
CREATE INDEX agent_instance_user_id_id_idx ON agent_instance (user_id, id);
ALTER TABLE agent_instance DROP CONSTRAINT IF EXISTS agent_instance_deleted_check;
ALTER TABLE agent_instance DROP CONSTRAINT IF EXISTS agent_instance_executor_check;
ALTER TABLE agent_instance DROP CONSTRAINT IF EXISTS agent_instance_state_check;
ALTER TABLE agent_instance ADD CONSTRAINT agent_instance_state_check CHECK (state IN (
    'AGENT_INSTANCE_STATE_CREATING', 'AGENT_INSTANCE_STATE_READY',
    'AGENT_INSTANCE_STATE_SUSPENDED', 'AGENT_INSTANCE_STATE_FAILED'));
ALTER TABLE agent_instance DROP COLUMN IF EXISTS pinned_checkpoint_id;
ALTER TABLE agent_instance DROP COLUMN IF EXISTS executor_id;
ALTER TABLE agent_instance DROP COLUMN IF EXISTS operation_id;
ALTER TABLE agent_instance ADD CONSTRAINT agent_instance_source_checkpoint_id_fkey
    FOREIGN KEY (source_checkpoint_id) REFERENCES agent_instance_checkpoint(id) ON DELETE RESTRICT;
ALTER TABLE agent_instance_checkpoint ADD COLUMN IF NOT EXISTS source_name TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_revision DROP CONSTRAINT IF EXISTS runtime_revision_credentials_check;
ALTER TABLE runtime_revision DROP COLUMN IF EXISTS credentials;
