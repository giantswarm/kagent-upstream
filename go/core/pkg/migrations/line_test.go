package migrations

import (
	"context"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"
)

// The line's source migrates after the built-in ones, in their schema and
// under its own tracking table, and its down leaves core's tables as core
// made them. Core's source carries none of it.
func TestLineMigrationsRoundTrip(t *testing.T) {
	dsn := startTestDB(t)
	sources := append(BuiltinSourcesInSchema(false, "public", "public"), LineSources("public")...)
	if err := RunUp(context.Background(), dsn, sources); err != nil {
		t.Fatalf("RunUp: %v", err)
	}
	if err := VerifyMigrated(context.Background(), dsn, sources); err != nil {
		t.Fatalf("VerifyMigrated: %v", err)
	}
	if versions := testVersions(t, dsn, lineTrackingTable); !slices.Equal(versions, []int64{0, 1}) {
		t.Fatalf("line versions = %v, want [0 1]", versions)
	}
	grantColumns := func(want bool) {
		t.Helper()
		for _, table := range []string{"agent_definition", "sandbox_template_definition"} {
			for _, column := range []string{"grant_destinations", "grant_credentials"} {
				if got := testColumnExists(t, dsn, table, column); got != want {
					t.Fatalf("%s.%s exists = %t, want %t", table, column, got, want)
				}
			}
		}
	}
	grantColumns(true)

	line := LineSources("public")[0]
	if err := WithProvider(context.Background(), dsn, line, func(provider *goose.Provider) error {
		_, err := provider.DownTo(context.Background(), 0)
		return err
	}); err != nil {
		t.Fatalf("down %s: %v", line.Name, err)
	}
	grantColumns(false)
	if err := VerifyMigrated(context.Background(), dsn, BuiltinSourcesInSchema(false, "public", "public")); err != nil {
		t.Fatalf("core after the line's down: %v", err)
	}
	if err := RunUp(context.Background(), dsn, sources); err != nil {
		t.Fatalf("second RunUp: %v", err)
	}
	grantColumns(true)
}
