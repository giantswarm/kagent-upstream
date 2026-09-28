-- +goose Up

-- A share may carry an expiry; its token grants nothing past it. NULL keeps a
-- share valid until it is revoked or its instance deleted.
ALTER TABLE agent_instance_share ADD COLUMN expires_at TIMESTAMPTZ;

-- +goose Down

ALTER TABLE agent_instance_share DROP COLUMN expires_at;
