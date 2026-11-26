package migrations

import (
	"embed"

	"github.com/kagent-dev/kagent/go/core/pkg/consts"
)

// LineFS embeds the Giant Swarm line's own migrations: the schema its carried
// patches add, kept out of upstream's core source, which upstream rewrites in
// place before its release.
//
//go:embed giantswarm
var LineFS embed.FS

const lineTrackingTable = "giantswarm_schema_migrations"

// LineSources returns the line's migration source in schema (the default
// table schema when empty), for app.Options.ExtraMigrations: it runs after the
// built-in sources, whose tables it alters.
func LineSources(schema string) []Source {
	if schema == "" {
		schema = consts.DefaultPostgresTableSchema
	}
	return []Source{{
		Name:          "giantswarm",
		Schema:        schema,
		TrackingTable: lineTrackingTable,
		FS:            LineFS,
		Dir:           "giantswarm",
	}}
}
