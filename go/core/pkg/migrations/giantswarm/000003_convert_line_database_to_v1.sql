-- +goose Up

-- A database the line's 1.x controller migrated holds core versions 1 to 7:
-- upstream's 000001 as it was at the line's previous pin, then the line's own
-- 2 to 7. Upstream's core track now has 000001 edited in place, 000002 and
-- 000003, and goose never applies a version at or below the highest one a
-- database holds, so on such a database the core track does nothing. This step
-- brings that database to the core track's shape, keeps what the line's
-- features stored, and leaves core's tracking table at upstream's versions
-- only, so upstream's next core version applies. A database the core track
-- created holds no line version above 3 and gets only the line's own schema.

-- +goose StatementBegin
CREATE FUNCTION pg_temp.gs_pb_varint(b bytea, pos int, OUT val bigint, OUT next int)
LANGUAGE plpgsql IMMUTABLE STRICT AS $fn$
DECLARE
    shift int := 0;
    octet int;
BEGIN
    val := 0;
    next := pos;
    LOOP
        IF next >= length(b) THEN
            RAISE EXCEPTION 'protobuf: truncated varint at offset %', pos;
        END IF;
        octet := get_byte(b, next);
        next := next + 1;
        val := val | ((octet & 127)::bigint << shift);
        EXIT WHEN octet < 128;
        shift := shift + 7;
        IF shift > 56 THEN
            RAISE EXCEPTION 'protobuf: varint too long at offset %', pos;
        END IF;
    END LOOP;
END
$fn$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION pg_temp.gs_pb_enc_varint(v bigint) RETURNS bytea
LANGUAGE plpgsql IMMUTABLE STRICT AS $fn$
DECLARE
    result bytea := ''::bytea;
BEGIN
    IF v < 0 THEN
        RAISE EXCEPTION 'protobuf: negative length %', v;
    END IF;
    LOOP
        IF v < 128 THEN
            RETURN result || set_byte('\x00'::bytea, 0, v::int);
        END IF;
        result := result || set_byte('\x00'::bytea, 0, ((v & 127) | 128)::int);
        v := v >> 7;
    END LOOP;
END
$fn$;
-- +goose StatementEnd

-- The top-level fields of a message in wire order: field number, wire type,
-- the field's byte range [fstart, fend) and, for a length-delimited field, its
-- payload's start and length. Offsets are zero-based.
-- +goose StatementBegin
CREATE FUNCTION pg_temp.gs_pb_fields(b bytea)
RETURNS TABLE (num bigint, wt int, fstart int, fend int, pstart int, plen int)
LANGUAGE plpgsql IMMUTABLE STRICT AS $fn$
DECLARE
    pos int := 0;
    tag bigint;
    len bigint;
BEGIN
    WHILE pos < length(b) LOOP
        fstart := pos;
        SELECT v.val, v.next INTO tag, pos FROM pg_temp.gs_pb_varint(b, pos) v;
        num := tag >> 3;
        wt := (tag & 7)::int;
        pstart := NULL;
        plen := NULL;
        CASE wt
            WHEN 0 THEN SELECT v.next INTO pos FROM pg_temp.gs_pb_varint(b, pos) v;
            WHEN 1 THEN pos := pos + 8;
            WHEN 2 THEN
                SELECT v.val, v.next INTO len, pos FROM pg_temp.gs_pb_varint(b, pos) v;
                pstart := pos;
                plen := len::int;
                pos := pos + len::int;
            WHEN 5 THEN pos := pos + 4;
            ELSE RAISE EXCEPTION 'protobuf: unsupported wire type % at offset %', wt, fstart;
        END CASE;
        IF num < 1 OR pos > length(b) THEN
            RAISE EXCEPTION 'protobuf: malformed field at offset %', fstart;
        END IF;
        fend := pos;
        RETURN NEXT;
    END LOOP;
END
$fn$;
-- +goose StatementEnd

-- kagent.api.v1alpha1 Session field 15 is ResourceReference agent; in
-- kagent.api.v1 it is AgentProvenance agent {ResourceReference ref = 1;
-- string uid = 2;}. The old payload becomes ref, byte for byte. Every other
-- field, the line's 16 to 21 included, keeps its bytes and its place.
-- +goose StatementBegin
CREATE FUNCTION pg_temp.gs_session_v1(b bytea, uid text) RETURNS bytea
LANGUAGE plpgsql IMMUTABLE STRICT AS $fn$
DECLARE
    f record;
    result bytea := ''::bytea;
    ref bytea;
    provenance bytea;
    uid_bytes bytea := convert_to(uid, 'UTF8');
BEGIN
    FOR f IN SELECT * FROM pg_temp.gs_pb_fields(b) ORDER BY fstart LOOP
        IF f.num = 15 AND f.wt = 2 THEN
            ref := substring(b FROM f.pstart + 1 FOR f.plen);
            provenance := '\x0a'::bytea || pg_temp.gs_pb_enc_varint(length(ref)) || ref;
            IF length(uid_bytes) > 0 THEN
                provenance := provenance || '\x12'::bytea || pg_temp.gs_pb_enc_varint(length(uid_bytes)) || uid_bytes;
            END IF;
            result := result || '\x7a'::bytea || pg_temp.gs_pb_enc_varint(length(provenance)) || provenance;
        ELSE
            result := result || substring(b FROM f.fstart + 1 FOR f.fend - f.fstart);
        END IF;
    END LOOP;
    RETURN result;
END
$fn$;
-- +goose StatementEnd

-- kagent.api.v1alpha1 SessionShare is {id 1, session_id 2, permission 3,
-- created_at 4, expires_at 5}; kagent.api.v1 renumbered it to {id 1,
-- session_id 2, created_at 3, expires_at 4}. The permission moves to
-- session_share_scope, the timestamps keep their bytes.
-- +goose StatementBegin
CREATE FUNCTION pg_temp.gs_session_share_v1(b bytea) RETURNS bytea
LANGUAGE plpgsql IMMUTABLE STRICT AS $fn$
DECLARE
    f record;
    result bytea := ''::bytea;
BEGIN
    FOR f IN SELECT * FROM pg_temp.gs_pb_fields(b) ORDER BY fstart LOOP
        IF f.num = 3 AND f.wt = 0 THEN
            CONTINUE;
        ELSIF f.num IN (4, 5) AND f.wt = 2 THEN
            result := result || CASE f.num WHEN 4 THEN '\x1a'::bytea ELSE '\x22'::bytea END
                || pg_temp.gs_pb_enc_varint(f.plen) || substring(b FROM f.pstart + 1 FOR f.plen);
        ELSE
            result := result || substring(b FROM f.fstart + 1 FOR f.fend - f.fstart);
        END IF;
    END LOOP;
    RETURN result;
END
$fn$;
-- +goose StatementEnd

-- The writable scope of a share. Upstream's shares grant read access only; a
-- share with a row here whose writable is true also lets its holder send turns.
CREATE TABLE IF NOT EXISTS session_share_scope (
    share_id UUID PRIMARY KEY REFERENCES session_share(id) ON DELETE CASCADE,
    writable BOOLEAN NOT NULL
);

-- The quiescence claim's lease: the holder renews it while it works; a claim
-- whose lease has run out, or that has none, is claimed again.
ALTER TABLE session_task_event ADD COLUMN IF NOT EXISTS quiescence_claimed_until TIMESTAMPTZ;

-- +goose StatementBegin
DO $lease$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
            WHERE conrelid = 'session_task_event'::regclass AND conname = 'session_task_event_quiescence_lease_held') THEN
        ALTER TABLE session_task_event ADD CONSTRAINT session_task_event_quiescence_lease_held
            CHECK (quiescence_claimed_until IS NULL OR quiescence_executor_id IS NOT NULL);
    END IF;
END
$lease$;
-- +goose StatementEnd

-- The generation of a quiescence holder's fencing token: every Pause, Suspend
-- and fencing Resume takes the next value, so Substrate refuses a late request
-- of a holder another one replaced.
CREATE SEQUENCE IF NOT EXISTS quiescence_fencing_generation;

-- The expiration sweep deletes the sessions of a deleted Agent with a reason of
-- their own.
ALTER TABLE session DROP CONSTRAINT IF EXISTS session_deletion_reason_check;
ALTER TABLE session ADD CONSTRAINT session_deletion_reason_check
    CHECK (deletion_reason IN ('user_requested', 'idle_timeout', 'agent_deleted'));

-- +goose StatementBegin
DO $convert$
DECLARE
    line_database boolean;
    ambiguous bigint;
BEGIN
    SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version_id BETWEEN 4 AND 7 AND is_applied)
        INTO line_database;
    IF NOT line_database THEN
        RETURN;
    END IF;

    -- Upstream's 000001 as edited in place since the line's previous pin.
    ALTER TABLE runtime_revision
        ADD COLUMN IF NOT EXISTS token_passthrough_targets JSONB NOT NULL DEFAULT '[]'
            CHECK (jsonb_typeof(token_passthrough_targets) = 'array'),
        ADD COLUMN IF NOT EXISTS actor_template_spec_digest TEXT NOT NULL DEFAULT '',
        ADD COLUMN IF NOT EXISTS golden_retries INT NOT NULL DEFAULT 0 CHECK (golden_retries >= 0),
        ADD COLUMN IF NOT EXISTS golden_failure_uid TEXT NOT NULL DEFAULT '',
        ADD COLUMN IF NOT EXISTS golden_failed_at TIMESTAMPTZ;
    -- Derived from the Agent and recorded again by the controller's next
    -- reconcile of each revision; token_passthrough_targets replaces it.
    DROP VIEW IF EXISTS agent_runtime_revision;
    ALTER TABLE runtime_revision DROP COLUMN IF EXISTS caller_credential;
    -- Columns in the order a new database's r.* expands to, since the added
    -- ones sit at the end of this table.
    CREATE VIEW agent_runtime_revision AS
        SELECT r.revision, r.kind, r.namespace, r.source_snapshot, r.egress_destinations, r.credentials,
            r.token_passthrough_targets, r.actor_template_atespace, r.actor_template_name, r.actor_template_uid,
            r.actor_template_spec_digest, r.golden_retries, r.golden_failure_uid, r.golden_failed_at,
            r.created_at, r.updated_at, r.deleted_at, a.agent_name, a.agent_uid, a.agent_card
        FROM runtime_revision r JOIN agent_revision a USING (revision) WHERE r.kind = 'agent';

    ALTER TABLE agent_definition
        ADD COLUMN IF NOT EXISTS grant_destinations TEXT[] NOT NULL DEFAULT '{}',
        ADD COLUMN IF NOT EXISTS grant_credentials JSONB NOT NULL DEFAULT '[]'
            CHECK (jsonb_typeof(grant_credentials) = 'array');
    ALTER TABLE sandbox_template_definition
        ADD COLUMN IF NOT EXISTS grant_destinations TEXT[] NOT NULL DEFAULT '{}',
        ADD COLUMN IF NOT EXISTS grant_credentials JSONB NOT NULL DEFAULT '[]'
            CHECK (jsonb_typeof(grant_credentials) = 'array');
    CREATE INDEX IF NOT EXISTS agent_revision_source_idx ON agent_revision (agent_name, revision);
    CREATE INDEX IF NOT EXISTS sandbox_revision_source_idx ON sandbox_revision (sandbox_template_name, revision);
    CREATE INDEX IF NOT EXISTS runtime_instance_revision_idx ON runtime_instance (prepared_revision, id)
        WHERE state <> 'RUNTIME_STATE_DELETED' AND operation <> 'RUNTIME_OPERATION_DELETE';

    -- Snapshot content scope becomes snapshot fidelity. Every snapshot the line
    -- recorded resumes from its durable volumes on the Substrate 2.x line,
    -- which converts a FULL or a DATA snapshot to a VOLUMES one, and kagent
    -- restores and forks from VOLUMES only.
    ALTER TABLE session_checkpoint DROP CONSTRAINT IF EXISTS session_checkpoint_snapshot_content_scope_check;
    ALTER TABLE session_checkpoint RENAME COLUMN snapshot_content_scope TO snapshot_fidelity;
    UPDATE session_checkpoint SET snapshot_fidelity = 'SNAPSHOT_FIDELITY_VOLUMES'
        WHERE snapshot_fidelity IN ('FULL', 'DATA');
    ALTER TABLE session_checkpoint ADD CONSTRAINT session_checkpoint_snapshot_fidelity_check
        CHECK (snapshot_fidelity IN ('SNAPSHOT_FIDELITY_MEMORY', 'SNAPSHOT_FIDELITY_ROOTFS', 'SNAPSHOT_FIDELITY_VOLUMES'));
    ALTER TABLE session_task RENAME COLUMN snapshot_content_scope TO snapshot_fidelity;
    UPDATE session_task SET snapshot_fidelity = 'SNAPSHOT_FIDELITY_VOLUMES'
        WHERE snapshot_fidelity IN ('FULL', 'DATA');
    ALTER TABLE session_task_event RENAME COLUMN snapshot_content_scope TO snapshot_fidelity;
    UPDATE session_task_event SET snapshot_fidelity = 'SNAPSHOT_FIDELITY_VOLUMES'
        WHERE snapshot_fidelity IN ('FULL', 'DATA');

    -- Upstream's 000002 and 000003.
    ALTER TABLE agent_revision ADD COLUMN IF NOT EXISTS bootstrap_bundle BYTEA
        CHECK (octet_length(bootstrap_bundle) <= 263168);
    ALTER TABLE runtime_instance DROP CONSTRAINT runtime_instance_operation_check;
    ALTER TABLE runtime_instance ADD CONSTRAINT runtime_instance_operation_check
        CHECK (operation IN ('RUNTIME_OPERATION_NONE', 'RUNTIME_OPERATION_CREATE', 'RUNTIME_OPERATION_SUSPEND',
            'RUNTIME_OPERATION_RESUME', 'RUNTIME_OPERATION_DELETE', 'RUNTIME_OPERATION_REVERT'));

    -- Shares: the permission moves to session_share_scope and the stored
    -- SessionShare takes kagent.api.v1's numbering.
    INSERT INTO session_share_scope (share_id, writable)
        SELECT id, permission = 'SESSION_SHARE_PERMISSION_READ_WRITE' FROM session_share
        ON CONFLICT (share_id) DO NOTHING;
    ALTER TABLE session_share DROP COLUMN permission;
    UPDATE session_share SET data = pg_temp.gs_session_share_v1(data);

    -- Sessions: field 15 becomes AgentProvenance, its uid the Agent's at the
    -- Session's prepared revision.
    UPDATE session s SET data = pg_temp.gs_session_v1(s.data, COALESCE((
            SELECT ar.agent_uid FROM runtime_instance ri
            JOIN agent_revision ar ON ar.revision = ri.prepared_revision
            WHERE ri.id = s.id), ''));

    -- Push: the outbox's undelivered rows become one pending delivery per
    -- registration at the newest undelivered event; upstream delivers the
    -- task's status as published at that point.
    ALTER TABLE session_push_registration
        ADD COLUMN pending_event_sequence BIGINT,
        ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
        ADD COLUMN next_attempt_at TIMESTAMPTZ,
        ADD COLUMN claim_token UUID,
        ADD COLUMN lease_until TIMESTAMPTZ;
    UPDATE session_push_registration p
        SET pending_event_sequence = o.sequence, next_attempt_at = clock_timestamp()
        FROM (SELECT registration_id, max(source_event_sequence) AS sequence FROM session_push_outbox
              WHERE state IN ('pending', 'sending') GROUP BY registration_id) o
        WHERE p.id = o.registration_id AND p.closed_at IS NULL AND p.task_id IS NOT NULL;
    DROP TABLE session_push_outbox;
    ALTER TABLE session_push_registration
        DROP CONSTRAINT session_push_registration_id_history_id_task_id_key,
        DROP COLUMN revision,
        ADD FOREIGN KEY (pending_event_sequence, history_id, task_id)
            REFERENCES session_task_event(sequence, history_id, task_id),
        ADD CHECK (pending_event_sequence IS NULL OR (task_id IS NOT NULL AND closed_at IS NULL)),
        ADD CHECK ((pending_event_sequence IS NULL AND attempt_count = 0 AND next_attempt_at IS NULL
                AND claim_token IS NULL AND lease_until IS NULL)
            OR (pending_event_sequence IS NOT NULL AND next_attempt_at IS NOT NULL)),
        ADD CHECK ((claim_token IS NULL AND lease_until IS NULL)
            OR (claim_token IS NOT NULL AND lease_until IS NOT NULL AND attempt_count > 0));
    CREATE INDEX session_push_registration_due ON session_push_registration (next_attempt_at, id)
        WHERE pending_event_sequence IS NOT NULL AND claim_token IS NULL;
    CREATE INDEX session_push_registration_lease ON session_push_registration (lease_until, id)
        WHERE claim_token IS NOT NULL;

    -- Long-term memory: the runtime wrote <namespace>__NS__<name> with every
    -- "-" as "_", or the agent card's name alone; upstream reads
    -- <namespace>/<name>. Kubernetes names hold no "_", so the rewrite is exact.
    IF to_regclass('memory') IS NOT NULL THEN
        UPDATE memory SET agent_name =
                replace(split_part(agent_name, '__NS__', 1), '_', '-') || '/' ||
                replace(split_part(agent_name, '__NS__', 2), '_', '-')
            WHERE strpos(agent_name, '__NS__') > 0;
        SELECT count(*) INTO ambiguous FROM memory m
            WHERE strpos(m.agent_name, '/') = 0 AND (
                SELECT count(DISTINCT d.namespace) FROM agent_definition d
                WHERE d.agent_name = replace(m.agent_name, '_', '-')) > 1;
        IF ambiguous > 0 THEN
            RAISE EXCEPTION 'memory: % rows name an agent without its namespace, and several namespaces hold an agent of that name', ambiguous;
        END IF;
        UPDATE memory m SET agent_name = d.namespace || '/' || d.agent_name
            FROM (SELECT DISTINCT namespace, agent_name FROM agent_definition) d
            WHERE strpos(m.agent_name, '/') = 0 AND d.agent_name = replace(m.agent_name, '_', '-');
    END IF;

    -- Core's tracking table names upstream's versions only: 2 and 3 now stand
    -- for upstream's 000002 and 000003, whose schema this step applied.
    DELETE FROM schema_migrations WHERE version_id BETWEEN 4 AND 7;
END
$convert$;
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
DO $down$
BEGIN
    RAISE EXCEPTION 'the line database conversion is one-way: restore the backup taken before the upgrade';
END
$down$;
-- +goose StatementEnd
