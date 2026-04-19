// Package aggregate ingests resolver scorecards/manifests/sweep-CSVs into
// a DuckDB store for cross-run analysis. The real implementation lives
// behind `//go:build duckdb`; a pure-Go stub returning ErrNotBuilt ships
// in the default (no-tag) build so the `resolver aggregate` subcommand
// can report a clean error without requiring CGO.
//
// See docs/adr/0002-duckdb-behind-build-tag.md and docs/build.md.
package aggregate

// Options is the common call surface for aggregate.Run, present in both
// build configurations. The concrete Run implementation differs by tag.
type Options struct {
	// ReportsDir is a comma-separated list of report roots to walk.
	// Empty → default ["reports", "research/captures"].
	ReportsDir string

	// DBPath is the DuckDB file to upsert into. Empty → reports/resolver.duckdb.
	DBPath string

	// CommunityBenchmarks is the YAML file to truncate-and-reload into the
	// community_benchmarks table. Empty → skip (the table is left untouched).
	CommunityBenchmarks string

	// DryRun prints the discovery set and exits without writing.
	DryRun bool
}
