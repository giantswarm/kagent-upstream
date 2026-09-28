-- +goose Up

-- A share may carry an expiry; its token grants nothing past it. NULL keeps a
-- share valid until it is revoked or its instance deleted. IF NOT EXISTS keeps
-- the migration a no-op on a baseline that already has the column.
ALTER TABLE agent_instance_share ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE agent_instance_share DROP COLUMN IF EXISTS expires_at;
