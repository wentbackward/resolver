//go:build duckdb

package aggregate_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/wentbackward/resolver/internal/aggregate"
)

// TestAggregateSchema_Migrate_V1_To_V2 asserts the migration path is
// idempotent — running aggregate.Run twice on a fresh empty corpus
// creates the v2 tables/views once, then leaves them alone on the
// second pass. This guards against pre-mortem scenario E (partial
// DDL persisted from a panic mid-migration).
func TestAggregateSchema_Migrate_V1_To_V2(t *testing.T) {
	reportsDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "migrate.duckdb")

	for i := 0; i < 2; i++ {
		if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}); err != nil {
			t.Fatalf("aggregate.Run pass %d: %v", i+1, err)
		}
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	// schemaVersion row lands exactly once (migrate() DELETEs then INSERTs).
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM _meta`).Scan(&n); err != nil {
		t.Fatalf("count _meta: %v", err)
	}
	if n != 1 {
		t.Errorf("_meta rows: got %d, want 1 after 2 migrate() passes", n)
	}

	var ver int
	if err := db.QueryRow(`SELECT schema_version FROM _meta`).Scan(&ver); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if ver != 2 {
		t.Errorf("schema_version: got %d, want 2", ver)
	}

	// role_scorecards table present + queryable.
	if _, err := db.Exec(`SELECT run_id, role FROM role_scorecards LIMIT 0`); err != nil {
		t.Errorf("role_scorecards not migrated: %v", err)
	}
}

// TestRoleScorecardsTable_PrimaryKey asserts the (run_id, role) PK
// actually rejects duplicate pairs — the idempotent DELETE-before-tx
// strategy only works if the PK is enforced.
func TestRoleScorecardsTable_PrimaryKey(t *testing.T) {
	reportsDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "pk.duckdb")
	if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}); err != nil {
		t.Fatalf("aggregate.Run: %v", err)
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ins := `INSERT INTO role_scorecards (run_id, role, verdict, threshold_met, threshold,
		metrics_json, scenario_count_expected, scenario_count_observed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := db.Exec(ins, "r1", "agentic-toolcall", "PASS", true, 0.9, "{}", 4, 4); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.Exec(ins, "r1", "agentic-toolcall", "PASS", true, 0.9, "{}", 4, 4); err == nil {
		t.Error("duplicate (run_id, role) insert succeeded; PK is not enforced")
	}
	// Different role → allowed.
	if _, err := db.Exec(ins, "r1", "reducer-json", "FAIL", false, 0.9, "{}", 4, 2); err != nil {
		t.Errorf("second role for same run_id rejected: %v", err)
	}
}
