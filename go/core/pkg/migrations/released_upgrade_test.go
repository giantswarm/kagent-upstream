package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	nurl "net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// releasedMigrationsEnv names a directory with one subdirectory per release,
// each holding that release's core and vector migrations
// (scripts/fork/released-migrations.sh exports them from the release tags).
const releasedMigrationsEnv = "KAGENT_RELEASED_MIGRATIONS"

// allowedReleasedExtras are the schema lines a migrated release may keep
// although a fresh install lacks them, each for a documented reason.
var allowedReleasedExtras = []string{
	// 000002 keeps the checkpoint source name: the 1.0 controller reads and
	// writes it, the current controller never names it.
	"column agent_instance_checkpoint.source_name text NOT NULL DEFAULT ''::text",
	"constraint agent_instance_checkpoint agent_instance_checkpoint_source_name_not_null NOT NULL source_name",
}

// releasedSources returns the migration sources of the release in dir, as
// that release recorded them.
func releasedSources(dir string) []Source {
	var sources []Source
	for _, src := range BuiltinSources(true) {
		if _, err := os.Stat(filepath.Join(dir, src.Dir)); err != nil {
			continue
		}
		src.FS = os.DirFS(dir)
		sources = append(sources, src)
	}
	return sources
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

// TestUpgradesEveryReleasedSchema migrates a database created by each
// released migration set with the current migrations and requires the schema
// a fresh install creates. A schema change folded into a released migration
// fails here until a new migration brings released databases along.
func TestUpgradesEveryReleasedSchema(t *testing.T) {
	root := os.Getenv(releasedMigrationsEnv)
	if root == "" {
		t.Skip(releasedMigrationsEnv + " is not set")
	}
	releases, err := fs.ReadDir(os.DirFS(root), ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) == 0 {
		t.Fatalf("%s has no release", root)
	}

	ctx := context.Background()
	server := startTestDB(t)
	current := BuiltinSources(true)
	fresh := testDatabase(t, server, "fresh")
	if err := RunUp(ctx, fresh, current); err != nil {
		t.Fatalf("fresh RunUp: %v", err)
	}
	want := testSchema(t, fresh)

	for i, release := range releases {
		t.Run(release.Name(), func(t *testing.T) {
			released := releasedSources(filepath.Join(root, release.Name()))
			if len(released) == 0 {
				t.Fatalf("%s holds no migrations", release.Name())
			}
			db := testDatabase(t, server, fmt.Sprintf("release_%d", i))
			if err := RunUp(ctx, db, released); err != nil {
				t.Fatalf("%s RunUp: %v", release.Name(), err)
			}
			if err := RunUp(ctx, db, current); err != nil {
				t.Fatalf("RunUp over %s: %v", release.Name(), err)
			}
			got := slices.DeleteFunc(testSchema(t, db), func(line string) bool {
				return slices.Contains(allowedReleasedExtras, line) && !slices.Contains(want, line)
			})
			if diff := schemaDiff(want, got); diff != "" {
				t.Fatalf("a database created by %s and migrated differs from a fresh install "+
					"(- only fresh, + only migrated); add a migration that brings it along:\n%s", release.Name(), diff)
			}
		})
	}
}
