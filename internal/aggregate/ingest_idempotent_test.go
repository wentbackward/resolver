//go:build duckdb

package aggregate_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/wentbackward/resolver/internal/aggregate"
)

// TestIngestIdempotency re-runs aggregate.Run against the same corpus
// twice and asserts no error plus stable row counts — guards against
// the DuckDB ART-index limitation where DELETE + INSERT of the same PK
// inside one transaction raises a constraint error.
func TestIngestIdempotency(t *testing.T) {
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "fixture", "virt")
	writeRun(t, runDir, v2Manifest("run-idem-001", "1"), v2Sidecar())

	communityYAML := `entries:
  - model: FixtureOrg/FixtureModel
    benchmark: bfcl
    metric: overall
    value: 0.5
    source_url: https://example.com
    as_of: 2026-04-01
`
	communityPath := filepath.Join(t.TempDir(), "community.yaml")
	if err := os.WriteFile(communityPath, []byte(communityYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(t.TempDir(), "idempotent.duckdb")
	opts := aggregate.Options{
		ReportsDir:          reportsDir,
		DBPath:              dbPath,
		CommunityBenchmarks: communityPath,
	}

	if err := aggregate.Run(opts); err != nil {
		t.Fatalf("first aggregate.Run: %v", err)
	}
	first := countRows(t, dbPath)

	if err := aggregate.Run(opts); err != nil {
		t.Fatalf("second aggregate.Run (idempotency failed): %v", err)
	}
	second := countRows(t, dbPath)

	for table, want := range first {
		if got := second[table]; got != want {
			t.Errorf("row count drifted after re-ingest: %s: got %d, want %d", table, got, want)
		}
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var cbCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM community_benchmarks`).Scan(&cbCount); err != nil {
		t.Fatal(err)
	}
	if cbCount != 1 {
		t.Errorf("community_benchmarks: got %d rows after re-ingest, want 1", cbCount)
	}
}
