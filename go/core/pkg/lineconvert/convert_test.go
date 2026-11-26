package lineconvert

import (
	"context"
	"embed"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	testcontainers "github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	apiv1 "github.com/kagent-dev/kagent/go/api/gen/kagent/api/v1"
	"github.com/kagent-dev/kagent/go/core/pkg/migrations"
	"github.com/kagent-dev/kagent/go/core/pkg/postgres"
)

// testdata/line-1 is the schema the line's 1.x controllers migrate: upstream's
// core 000001 at 92dd6caa, the line's core 2 to 7 and its giantswarm track 1
// and 2.
//
//go:embed testdata/line-1
var lineOneFS embed.FS

const (
	schema       = "kagent"
	vectorSchema = "public"
)

func startDB(t *testing.T) *pgxpool.Config {
	t.Helper()
	if testing.Short() {
		t.Skip("skip the PostgreSQL test in short mode")
	}
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg18-trixie",
		tcpostgres.WithDatabase("kagent_test"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("kagent"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	config, err := postgres.PoolConfig(dsn, "postgres")
	require.NoError(t, err)
	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	defer pool.Close()
	_, err = pool.Exec(ctx, "CREATE EXTENSION vector WITH SCHEMA public")
	require.NoError(t, err)
	return config
}

func lineOneSources() []migrations.Source {
	return []migrations.Source{
		{Name: "core", Schema: schema, VectorSchema: vectorSchema, TrackingTable: "schema_migrations", FS: lineOneFS, Dir: "testdata/line-1/core"},
		{Name: "vector", Schema: schema, VectorSchema: vectorSchema, TrackingTable: "vector_schema_migrations", FS: migrations.FS, Dir: "vector"},
		{Name: "giantswarm", Schema: schema, TrackingTable: "giantswarm_schema_migrations", FS: lineOneFS, Dir: "testdata/line-1/giantswarm"},
	}
}

func v1Sources(target string) []migrations.Source {
	return append(migrations.BuiltinSourcesInSchema(true, target, vectorSchema), migrations.LineSources(target)...)
}

func str(b []byte, num protowire.Number, v string) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendString(b, v)
}

func msg(b []byte, num protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, num, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func timestamp(t *testing.T, seconds int64) []byte {
	t.Helper()
	b, err := proto.Marshal(&timestamppb.Timestamp{Seconds: seconds})
	require.NoError(t, err)
	return b
}

// lineSession is a Session as the line's 1.x controller stored it: field 15
// is ResourceReference{namespace, name}, 16 and 17 are the line's own.
func lineSession(t *testing.T, id string) []byte {
	b := str(nil, 1, id)
	b = str(b, 2, "alice")
	b = msg(b, 15, str(str(nil, 1, "team-a"), 2, "my-agent"))
	b = str(b, 13, "a conversation")
	b = msg(b, 16, str(nil, 1, "quiesce failed"))
	b = msg(b, 17, str(nil, 1, "workspace"))
	b = msg(b, 10, timestamp(t, 1700000000))
	return b
}

func lineShare(t *testing.T, id, session string, permission uint64, created, expires int64) []byte {
	b := str(nil, 1, id)
	b = str(b, 2, session)
	b = protowire.AppendTag(b, 3, protowire.VarintType)
	b = protowire.AppendVarint(b, permission)
	b = msg(b, 4, timestamp(t, created))
	if expires > 0 {
		b = msg(b, 5, timestamp(t, expires))
	}
	return b
}

type ids struct {
	session, deleted, history, deletedHistory, shareWrite, shareRead, open, closed uuid.UUID
}

func seed(t *testing.T, ctx context.Context, conn *pgxpool.Conn) ids {
	t.Helper()
	id := ids{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, query, args...)
		require.NoError(t, err, query)
	}
	exec(`INSERT INTO runtime_revision (revision, kind, namespace, source_snapshot, actor_template_atespace, actor_template_name, caller_credential)
		VALUES ('rev1', 'agent', 'team-a', '{}', 'team-a', 'tmpl1', '{"hosts":["a"]}')`)
	exec(`INSERT INTO agent_revision (revision, agent_name, agent_uid, agent_card) VALUES ('rev1', 'my-agent', 'uid-1', '\x00')`)
	exec(`INSERT INTO agent_definition (namespace, agent_name, agent_uid, desired_revision) VALUES ('team-a', 'my-agent', 'uid-1', 'rev1')`)
	for _, s := range []struct {
		session, history uuid.UUID
		state, revision  any
		reason           any
	}{
		{id.session, id.history, "RUNTIME_STATE_READY", "rev1", nil},
		{id.deleted, id.deletedHistory, "RUNTIME_STATE_DELETED", nil, "agent_deleted"},
	} {
		exec(`INSERT INTO a2a_context (id, context_id) VALUES ($1, $2)`, s.history, s.session)
		exec(`INSERT INTO runtime_instance (id, kind, user_id, request_id, prepared_revision, state) VALUES ($1, 'agent', 'alice', $2, $3, $4)`,
			s.session, s.session.String(), s.revision, s.state)
		exec(`INSERT INTO session (id, data, context_id, history_id, deletion_reason) VALUES ($1, $2, $1, $3, $4)`,
			s.session, lineSession(t, s.session.String()), s.history, s.reason)
	}
	exec(`INSERT INTO session_share (id, session_id, permission, token_hash, data, expires_at)
		VALUES ($1, $2, 'SESSION_SHARE_PERMISSION_READ_WRITE', '\x01', $3, now() + interval '1 day')`,
		id.shareWrite, id.session, lineShare(t, id.shareWrite.String(), id.session.String(), 2, 1700000100, 1800000000))
	exec(`INSERT INTO session_share (id, session_id, permission, token_hash, data)
		VALUES ($1, $2, 'SESSION_SHARE_PERMISSION_READ_ONLY', '\x02', $3)`,
		id.shareRead, id.session, lineShare(t, id.shareRead.String(), id.session.String(), 1, 1700000200, 0))
	exec(`INSERT INTO session_task (history_id, id, state, data, snapshot_atespace, snapshot_uri, snapshot_content_scope, history_sequence)
		VALUES ($1, 't1', 'TASK_STATE_COMPLETED', '\x00', 'team-a', 'store://task', 'FULL', 1)`, id.history)
	for _, scope := range []string{"DATA", "FULL", "DATA"} {
		exec(`INSERT INTO session_task_event (history_id, task_id, data, snapshot_atespace, snapshot_uri, snapshot_content_scope)
			VALUES ($1, 't1', '\x00', 'team-a', 'store://event-' || $2, $2)`, id.history, scope)
	}
	exec(`INSERT INTO session_checkpoint (id, source_session_id, user_id, request_id, head_task_id, history_sequence,
			snapshot_atespace, snapshot_uri, snapshot_content_scope, state, data, source_history_id, prepared_revision)
		VALUES ($1, $2, 'alice', 'c1', 't1', 1, 'team-a', 'store://checkpoint', 'FULL', 'READY', '\x00', $3, 'rev1')`,
		uuid.New(), id.session, id.history)
	exec(`INSERT INTO session_push_registration (id, history_id, config_id, url, task_id, revision) VALUES ($1, $2, 'open', 'https://push.example/a', 't1', 2)`,
		id.open, id.history)
	exec(`INSERT INTO session_push_registration (id, history_id, config_id, url, task_id, closed_at) VALUES ($1, $2, 'closed', 'https://push.example/b', 't1', now())`,
		id.closed, id.history)
	var sequences []int64
	rows, err := conn.Query(ctx, `SELECT sequence FROM session_task_event WHERE history_id = $1 ORDER BY sequence`, id.history)
	require.NoError(t, err)
	sequences, err = pgx.CollectRows(rows, pgx.RowTo[int64])
	require.NoError(t, err)
	outbox := func(registration uuid.UUID, sequence int64, state string) {
		t.Helper()
		switch state {
		case "delivered":
			exec(`INSERT INTO session_push_outbox (id, registration_id, history_id, task_id, config_revision, source_event_sequence, payload, state, delivered_at)
				VALUES ($1, $2, $3, 't1', 1, $4, '\x01', 'delivered', now())`, uuid.New(), registration, id.history, sequence)
		case "sending":
			exec(`INSERT INTO session_push_outbox (id, registration_id, history_id, task_id, config_revision, source_event_sequence, payload, state, attempt_count, claim_token, lease_until)
				VALUES ($1, $2, $3, 't1', 2, $4, '\x01', 'sending', 1, $5, now())`, uuid.New(), registration, id.history, sequence, uuid.New())
		default:
			exec(`INSERT INTO session_push_outbox (id, registration_id, history_id, task_id, config_revision, source_event_sequence, payload, state)
				VALUES ($1, $2, $3, 't1', 2, $4, '\x01', $5)`, uuid.New(), registration, id.history, sequence, state)
		}
	}
	outbox(id.open, sequences[0], "delivered")
	outbox(id.open, sequences[1], "sending")
	outbox(id.open, sequences[2], "pending")
	outbox(id.closed, sequences[2], "pending")
	for key, user := range map[string]string{
		"team_a__NS__my_agent": "alice",
		"team-a__NS__my-agent": "bob",
		"my_agent":             "carol",
		"gone_agent":           "dave",
	} {
		exec(`INSERT INTO memory (agent_name, user_id, content, created_at) VALUES ($1, $2, 'remember ' || $2, now())`, key, user)
	}
	return id
}

func acquire(t *testing.T, ctx context.Context, pool *pgxpool.Pool, searchPath string) *pgxpool.Conn {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, "SET search_path = "+pgx.Identifier{searchPath}.Sanitize()+", public")
	require.NoError(t, err)
	return conn
}

func TestConvertLineDatabase(t *testing.T) {
	ctx := t.Context()
	config := startDB(t)
	require.NoError(t, migrations.RunUp(ctx, config, lineOneSources()))
	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	defer pool.Close()
	conn := acquire(t, ctx, pool, schema)
	id := seed(t, ctx, conn)
	conn.Release()

	before, err := Inspect(ctx, pool, schema)
	require.NoError(t, err)
	require.Equal(t, ShapeLine, before.Shape)
	require.Equal(t, int64(1), before.Counts["memory_unresolved"])
	require.Equal(t, int64(0), before.Counts["memory_ambiguous"])
	require.Equal(t, int64(1), before.Counts["push_undelivered_closed"])

	require.NoError(t, migrations.RunUp(ctx, config, v1Sources(schema)))
	after, err := Inspect(ctx, pool, schema)
	require.NoError(t, err)
	require.Equal(t, ShapeV1, after.Shape)
	require.Empty(t, Compare(before, after))
	require.Equal(t, int64(0), after.Counts["sessions_undecodable"])
	require.Equal(t, int64(5), after.Counts["snapshot_snapshot_fidelity_volumes"])
	require.NoError(t, migrations.VerifyMigrated(ctx, config, v1Sources(schema)))

	conn = acquire(t, ctx, pool, schema)
	defer conn.Release()
	versions := func(table string) []int64 {
		rows, err := conn.Query(ctx, "SELECT version_id FROM "+table+" WHERE version_id > 0 ORDER BY version_id")
		require.NoError(t, err)
		v, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		require.NoError(t, err)
		return v
	}
	require.Equal(t, []int64{1, 2, 3}, versions("schema_migrations"), "core names upstream's versions only")
	require.Equal(t, []int64{1, 2, 3}, versions("giantswarm_schema_migrations"))

	t.Run("sessions decode as kagent.api.v1", func(t *testing.T) {
		for session, uid := range map[uuid.UUID]string{id.session: "uid-1", id.deleted: ""} {
			var data []byte
			require.NoError(t, conn.QueryRow(ctx, "SELECT data FROM session WHERE id = $1", session).Scan(&data))
			decoded := &apiv1.Session{}
			require.NoError(t, proto.Unmarshal(data, decoded))
			require.Equal(t, "team-a", decoded.GetAgent().GetRef().GetNamespace())
			require.Equal(t, "my-agent", decoded.GetAgent().GetRef().GetName())
			require.Equal(t, uid, decoded.GetAgent().GetUid())
			require.Equal(t, "a conversation", decoded.GetName())
			require.Equal(t, int64(1700000000), decoded.GetCreatedAt().GetSeconds())
			unknown := decoded.ProtoReflect().GetUnknown()
			require.Equal(t, msg(msg(nil, 16, str(nil, 1, "quiesce failed")), 17, str(nil, 1, "workspace")), []byte(unknown),
				"the line's fields 16 and 17 keep their bytes")
		}
		var reason string
		require.NoError(t, conn.QueryRow(ctx, "SELECT deletion_reason FROM session WHERE id = $1", id.deleted).Scan(&reason))
		require.Equal(t, "agent_deleted", reason)
	})

	t.Run("shares keep their scope and timestamps", func(t *testing.T) {
		for share, want := range map[uuid.UUID]struct {
			writable         bool
			created, expires int64
		}{id.shareWrite: {true, 1700000100, 1800000000}, id.shareRead: {false, 1700000200, 0}} {
			var data []byte
			var writable bool
			require.NoError(t, conn.QueryRow(ctx, `SELECT s.data, c.writable FROM session_share s
				JOIN session_share_scope c ON c.share_id = s.id WHERE s.id = $1`, share).Scan(&data, &writable))
			decoded := &apiv1.SessionShare{}
			require.NoError(t, proto.Unmarshal(data, decoded))
			require.Equal(t, want.writable, writable)
			require.Equal(t, share.String(), decoded.GetId())
			require.Equal(t, want.created, decoded.GetCreatedAt().GetSeconds())
			require.Equal(t, want.expires, decoded.GetExpiresAt().GetSeconds())
			require.Empty(t, decoded.ProtoReflect().GetUnknown())
		}
	})

	t.Run("the undelivered push backlog is armed once", func(t *testing.T) {
		var pending int64
		var armedClosed bool
		require.NoError(t, conn.QueryRow(ctx, `SELECT pending_event_sequence FROM session_push_registration WHERE id = $1`, id.open).Scan(&pending))
		var newest int64
		require.NoError(t, conn.QueryRow(ctx, `SELECT max(sequence) FROM session_task_event WHERE history_id = $1`, id.history).Scan(&newest))
		require.Equal(t, newest, pending)
		require.NoError(t, conn.QueryRow(ctx, `SELECT pending_event_sequence IS NOT NULL FROM session_push_registration WHERE id = $1`, id.closed).Scan(&armedClosed))
		require.False(t, armedClosed, "a closed registration delivers nothing")
	})

	t.Run("memory takes upstream's keys", func(t *testing.T) {
		rows, err := conn.Query(ctx, "SELECT user_id || '=' || agent_name FROM memory ORDER BY user_id")
		require.NoError(t, err)
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		require.Equal(t, []string{"alice=team-a/my-agent", "bob=team-a/my-agent", "carol=team-a/my-agent", "dave=gone_agent"}, got)
	})

	t.Run("a second run changes nothing", func(t *testing.T) {
		require.NoError(t, migrations.RunUp(ctx, config, v1Sources(schema)))
		again, err := Inspect(ctx, pool, schema)
		require.NoError(t, err)
		require.Equal(t, after, again)
	})

	t.Run("the schema equals a new database's", func(t *testing.T) {
		require.NoError(t, migrations.RunUp(ctx, config, v1Sources("fresh")))
		converted := catalog(t, ctx, pool, schema)
		fresh := catalog(t, ctx, pool, "fresh")
		var missing, extra []string
		for _, line := range fresh {
			if !slices.Contains(converted, line) {
				missing = append(missing, line)
			}
		}
		for _, line := range converted {
			if !slices.Contains(fresh, line) {
				extra = append(extra, line)
			}
		}
		require.Empty(t, missing, "in a new database, not in the converted one")
		require.Empty(t, extra, "in the converted database, not in a new one")
	})
}

// catalog lists the tables, columns, constraints, indexes, views and sequences
// of a schema in a form that names no schema and no generated constraint name.
func catalog(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) []string {
	t.Helper()
	conn := acquire(t, ctx, pool, name)
	defer conn.Release()
	query := func(q string) []string {
		rows, err := conn.Query(ctx, q, name)
		require.NoError(t, err)
		out, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		return out
	}
	var all []string
	all = append(all, query(`SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type || ' null=' || is_nullable
		|| ' default=' || COALESCE(column_default, '') FROM information_schema.columns
		WHERE table_schema = $1 AND table_name NOT LIKE '%schema_migrations'`)...)
	all = append(all, query(`SELECT 'constraint ' || c.relname || ' ' || k.contype::text || ' ' || pg_get_constraintdef(k.oid)
		FROM pg_constraint k JOIN pg_class c ON c.oid = k.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname NOT LIKE '%schema_migrations'`)...)
	all = append(all, query(`SELECT 'index ' || tablename || ' ' || replace(indexdef, schemaname || '.', '') FROM pg_indexes
		WHERE schemaname = $1 AND tablename NOT LIKE '%schema_migrations'`)...)
	all = append(all, query(`SELECT 'view ' || c.relname || ' ' || pg_get_viewdef(c.oid) FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relkind = 'v'`)...)
	all = append(all, query(`SELECT 'sequence ' || sequencename FROM pg_sequences WHERE schemaname = $1`)...)
	for i, line := range all {
		all[i] = strings.Join(strings.Fields(line), " ")
	}
	slices.Sort(all)
	return all
}

// A memory row naming an Agent without its namespace, when several namespaces
// hold an Agent of that name, cannot be keyed: the conversion refuses and the
// database stays as the line left it.
func TestAmbiguousMemoryRefusesTheConversion(t *testing.T) {
	ctx := t.Context()
	config := startDB(t)
	require.NoError(t, migrations.RunUp(ctx, config, lineOneSources()))
	pool, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	defer pool.Close()
	conn := acquire(t, ctx, pool, schema)
	seed(t, ctx, conn)
	_, err = conn.Exec(ctx, `INSERT INTO runtime_revision (revision, kind, namespace, source_snapshot, actor_template_atespace, actor_template_name)
		VALUES ('rev2', 'agent', 'team-b', '{}', 'team-b', 'tmpl2')`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO agent_revision (revision, agent_name, agent_uid, agent_card) VALUES ('rev2', 'my-agent', 'uid-2', '\x00')`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `INSERT INTO agent_definition (namespace, agent_name, agent_uid, desired_revision) VALUES ('team-b', 'my-agent', 'uid-2', 'rev2')`)
	require.NoError(t, err)
	conn.Release()

	before, err := Inspect(ctx, pool, schema)
	require.NoError(t, err)
	require.Equal(t, int64(1), before.Counts["memory_ambiguous"])
	err = migrations.RunUp(ctx, config, v1Sources(schema))
	require.ErrorContains(t, err, "several namespaces hold an agent of that name")
	after, err := Inspect(ctx, pool, schema)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestMemoryKey(t *testing.T) {
	namespaces := map[string][]string{"my-agent": {"team-a"}, "twin": {"a", "b", "a"}}
	for key, want := range map[string]struct {
		key   string
		state int
	}{
		"team_a__NS__my_agent": {"team-a/my-agent", keyResolved},
		"team-a__NS__my-agent": {"team-a/my-agent", keyResolved},
		"team-a/my-agent":      {"team-a/my-agent", keyResolved},
		"my_agent":             {"team-a/my-agent", keyResolved},
		"twin":                 {"twin", keyAmbiguous},
		"gone":                 {"gone", keyUnresolved},
	} {
		got, state := MemoryKey(key, namespaces)
		require.Equal(t, want.key, got, key)
		require.Equal(t, want.state, state, key)
	}
}
