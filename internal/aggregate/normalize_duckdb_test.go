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

func TestCommunityJoinNormalized(t *testing.T) {
	yamlContent := `entries:
  - model: qwen3.6-35b-a3b
    benchmark: bfcl
    metric: overall
    value: 0.78
    source_url: https://gorilla.cs.berkeley.edu
    as_of: 2026-04-01
`
	communityPath := filepath.Join(t.TempDir(), "community-benchmarks.yaml")
	if err := os.WriteFile(communityPath, []byte(yamlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	reportsDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "join_test.duckdb")

	if err := aggregate.Run(aggregate.Options{
		ReportsDir:          reportsDir,
		DBPath:              dbPath,
		CommunityBenchmarks: communityPath,
	}); err != nil {
		t.Fatalf("aggregate.Run: %v", err)
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	var storedKey string
	if err := db.QueryRow(
		`SELECT model_key FROM community_benchmarks WHERE benchmark = 'bfcl'`,
	).Scan(&storedKey); err != nil {
		t.Fatalf("query model_key: %v", err)
	}
	wantKey := aggregate.NormalizeModel("qwen3.6-35b-a3b")
	if storedKey != wantKey {
		t.Errorf("stored model_key = %q, want %q", storedKey, wantKey)
	}

	runKey := aggregate.NormalizeModel("Qwen/Qwen3.6-35B-A3B-FP8")
	if runKey != storedKey {
		t.Errorf("NormalizeModel(vLLM name) = %q != stored model_key %q — join would miss", runKey, storedKey)
	}

	if _, err := db.Exec(`INSERT INTO runs (
		run_id, scorecard_path, manifest_path, model, resolved_real_model,
		overall, query_count, correct_count, partial_count, incorrect_count, error_count
	) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		"test-run-norm", "/sc/1", "/m/1", "test-virtual",
		"Qwen/Qwen3.6-35B-A3B-FP8",
		"PASS", 1, 1, 0, 0, 0,
	); err != nil {
		t.Fatalf("insert run: %v", err)
	}

	normalizedRunModel := aggregate.NormalizeModel("Qwen/Qwen3.6-35B-A3B-FP8")
	var matchCount int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM runs r
		JOIN community_benchmarks cb ON cb.model_key = ?
		WHERE r.run_id = 'test-run-norm'
	`, normalizedRunModel).Scan(&matchCount); err != nil {
		t.Fatalf("join query: %v", err)
	}
	if matchCount != 1 {
		t.Errorf("expected 1 row from community join, got %d", matchCount)
	}
}
