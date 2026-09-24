package migrations

import (
	"context"
	"database/sql"
	nurl "net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// core10Source is the version 1 baseline the Kagent 1.0 releases migrated,
// recorded in the core tracking table as those releases recorded it.
func core10Source() Source {
	return Source{
		Name:          "core",
		TrackingTable: coreTrackingTable,
		FS:            os.DirFS("testdata"),
		Dir:           "core-1.0",
	}
}

// testDatabase creates database name on the server behind dsn and returns its URL.
func testDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	execSQL(t, dsn, "CREATE DATABASE "+quoteIdentifier(name))
	u, err := nurl.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

func testQueryString(t *testing.T, dsn, query string, args ...any) string {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&value); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return value
}

// testSchema lists the current schema's columns, constraints, indexes and
// views, one sorted line each, independent of column order.
func testSchema(t *testing.T, dsn string) []string {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(context.Background(), `
		SELECT 'column ' || c.relname || '.' || a.attname || ' ' || format_type(a.atttypid, a.atttypmod)
			|| CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END
			|| COALESCE(' DEFAULT ' || pg_get_expr(d.adbin, d.adrelid), '')
			|| CASE WHEN a.attgenerated::text <> '' THEN ' GENERATED ' || a.attgenerated::text ELSE '' END
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE c.relnamespace = current_schema()::regnamespace AND c.relkind IN ('r', 'v')
			AND a.attnum > 0 AND NOT a.attisdropped
		UNION ALL
		SELECT 'constraint ' || conrelid::regclass || ' ' || conname || ' ' || pg_get_constraintdef(oid)
		FROM pg_constraint WHERE connamespace = current_schema()::regnamespace
		UNION ALL
		SELECT 'index ' || indexdef FROM pg_indexes WHERE schemaname = current_schema()
		UNION ALL
		SELECT 'view ' || viewname || ' ' || definition FROM pg_views WHERE schemaname = current_schema()
		ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// schemaDiff lists the lines only in want (-) and only in got (+).
func schemaDiff(want, got []string) string {
	var b strings.Builder
	for _, line := range want {
		if !slices.Contains(got, line) {
			b.WriteString("- " + line + "\n")
		}
	}
	for _, line := range got {
		if !slices.Contains(want, line) {
			b.WriteString("+ " + line + "\n")
		}
	}
	return b.String()
}

func TestCoreUpgradesThe1_0Baseline(t *testing.T) {
	ctx := context.Background()
	server := startTestDB(t)
	upgraded := testDatabase(t, server, "upgraded")
	fresh := testDatabase(t, server, "fresh")
	core := BuiltinSources(false)

	if err := RunUp(ctx, upgraded, []Source{core10Source()}); err != nil {
		t.Fatalf("1.0 RunUp: %v", err)
	}
	historyID := "00000000-0000-0000-0000-000000000001"
	forkHistoryID := "00000000-0000-0000-0000-000000000002"
	contextID := "00000000-0000-0000-0000-000000000003"
	checkpointID := "00000000-0000-0000-0000-000000000004"
	forkID := "00000000-0000-0000-0000-000000000005"
	execSQL(t, upgraded, `INSERT INTO runtime_revision (revision, namespace, agent_template_name, agent_template_uid,
		harness_name, harness_uid, source_snapshot, actor_template_atespace, actor_template_name, agent_card)
		VALUES ('rev-1', 'ns', 'template', 'uid', 'harness', 'uid', '{}', 'ns', 'actor-1', '')`)
	execSQL(t, upgraded, "INSERT INTO a2a_context (id, user_id, context_id) VALUES ($1, 'user', $2), ($3, 'user', $2)",
		historyID, contextID, forkHistoryID)
	execSQL(t, upgraded, `INSERT INTO agent_instance_checkpoint (id, source_instance_id, user_id, request_id, head_task_id,
		history_sequence, snapshot_atespace, snapshot_uri, snapshot_content_scope, state, data, source_history_id,
		prepared_revision, source_name)
		VALUES ($1, $2, 'user', 'checkpoint', 'task', 1, 'ns', 'uri', 'FULL', 'READY', '', $3, 'rev-1', 'agent')`,
		checkpointID, forkID, historyID)
	execSQL(t, upgraded, `INSERT INTO agent_instance (id, user_id, request_id, prepared_revision, state, data, context_id,
		history_id, source_checkpoint_id)
		VALUES ($1, 'user', 'fork', 'rev-1', 'AGENT_INSTANCE_STATE_READY', '', $2, $3, $4)`,
		forkID, contextID, forkHistoryID, checkpointID)

	if err := RunUp(ctx, upgraded, core); err != nil {
		t.Fatalf("RunUp over the 1.0 baseline: %v", err)
	}
	if got := testVersions(t, upgraded, coreTrackingTable); !slices.Equal(got, []int64{0, 1, 2}) {
		t.Fatalf("versions = %v", got)
	}
	if err := VerifyMigrated(ctx, upgraded, core); err != nil {
		t.Fatalf("VerifyMigrated: %v", err)
	}

	if err := WithProvider(ctx, fresh, core[0], func(provider *goose.Provider) error {
		_, err := provider.UpTo(ctx, 1)
		return err
	}); err != nil {
		t.Fatalf("UpTo 1: %v", err)
	}
	baseline := testSchema(t, fresh)
	if err := RunUp(ctx, fresh, core); err != nil {
		t.Fatalf("RunUp over the current baseline: %v", err)
	}
	current := testSchema(t, fresh)
	if diff := schemaDiff(baseline, current); diff != "" {
		t.Fatalf("000002 changed the current baseline:\n%s", diff)
	}

	// The 1.0 controller reads and writes the checkpoint source name.
	want := append(slices.Clone(current),
		"column agent_instance_checkpoint.source_name text NOT NULL DEFAULT ''::text",
		"constraint agent_instance_checkpoint agent_instance_checkpoint_source_name_not_null NOT NULL source_name",
	)
	if diff := schemaDiff(want, testSchema(t, upgraded)); diff != "" {
		t.Fatalf("the upgraded 1.0 schema differs from the current one:\n%s", diff)
	}

	if got := testQueryString(t, upgraded, "SELECT pinned_checkpoint_id::text FROM agent_instance WHERE id = $1", forkID); got != checkpointID {
		t.Fatalf("pinned_checkpoint_id = %s, want %s", got, checkpointID)
	}
	if got := testQueryString(t, upgraded, "SELECT credentials::text FROM runtime_revision WHERE revision = 'rev-1'"); got != "[]" {
		t.Fatalf("credentials = %s", got)
	}
	if got := testQueryString(t, upgraded, "SELECT source_name FROM agent_instance_checkpoint WHERE id = $1", checkpointID); got != "agent" {
		t.Fatalf("source_name = %q", got)
	}
	execSQL(t, upgraded, `INSERT INTO runtime_revision (revision, namespace, agent_template_name, agent_template_uid,
		harness_name, harness_uid, source_snapshot, actor_template_atespace, actor_template_name, agent_card, credentials)
		VALUES ('rev-2', 'ns', 'template', 'uid', 'harness', 'uid', '{}', 'ns', 'actor-2', '', '[{"name": "github"}]')`)
}

func TestCoreRefusesAMixedBaseline(t *testing.T) {
	ctx := context.Background()
	dsn := startTestDB(t)
	if err := RunUp(ctx, dsn, []Source{core10Source()}); err != nil {
		t.Fatalf("1.0 RunUp: %v", err)
	}
	execSQL(t, dsn, "ALTER TABLE agent_instance ADD COLUMN operation_id UUID")

	err := RunUp(ctx, dsn, BuiltinSources(false))
	if err == nil || !strings.Contains(err.Error(), "matches neither") {
		t.Fatalf("RunUp = %v, want the mixed baseline refused", err)
	}
	if got := testVersions(t, dsn, coreTrackingTable); !slices.Equal(got, []int64{0, 1}) {
		t.Fatalf("versions = %v", got)
	}
	if testColumnExists(t, dsn, "runtime_revision", "credentials") {
		t.Fatal("the refused migration added runtime_revision.credentials")
	}
}
