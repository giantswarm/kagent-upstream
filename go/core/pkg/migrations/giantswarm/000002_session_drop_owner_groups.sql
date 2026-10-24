-- +goose Up

-- No controller reads a session's owner_groups since the turn a share admits
-- stopped carrying the owner's groups: the groups of a caller come from the
-- caller's verified token on every call. Core's 000006 still adds the column
-- on a fresh database, so it goes here, after the built-in sources.
ALTER TABLE session DROP COLUMN IF EXISTS owner_groups;

-- +goose Down

ALTER TABLE session ADD COLUMN IF NOT EXISTS owner_groups TEXT[] NOT NULL DEFAULT '{}';
