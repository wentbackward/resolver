//go:build duckdb

package aggregate_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/wentbackward/resolver/internal/aggregate"
)

// TestViewColumnsStable is a cross-language schema-drift gate.
//
// The Python analyzer (tools/analyze/src/analyze/db.py) selects named
// columns out of the DuckDB views `run_summary` and `comparison`. A
// rename in internal/aggregate/schema.go would otherwise only blow up at
// analyzer runtime, not at Go CI. This test captures the ordered column
// set of each view into golden/view_columns.txt so any change to the
// view shape fails fast — either the rename is intentional (run
// `UPDATE_GOLDEN=1 go test -tags duckdb ./internal/aggregate/... -run
// TestViewColumnsStable` to refresh the golden) or it is a breaking
// drift against the Python side and must be reverted.
//
// Companion test on the Python side:
// tools/analyze/tests/test_schema_drift.py — asserts that every column
// db.py references actually exists in the view.
func TestViewColumnsStable(t *testing.T) {
	// Empty reports dir → aggregate.Run() creates tables + views and
	// returns without ingesting anything. That's enough to introspect
	// the view shape via PRAGMA table_info.
	reportsDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "drift.duckdb")
	if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}); err != nil {
		t.Fatalf("aggregate.Run: %v", err)
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	defer db.Close()

	views := []string{"run_summary", "comparison", "role_coverage"}
	var got strings.Builder
	for i, v := range views {
		if i > 0 {
			got.WriteByte('\n')
		}
		fmt.Fprintf(&got, "# %s\n", v)
		cols, err := viewColumns(db, v)
		if err != nil {
			t.Fatalf("pragma table_info(%s): %v", v, err)
		}
		for _, c := range cols {
			got.WriteString(c)
			got.WriteByte('\n')
		}
	}

	goldenPath := filepath.Join("..", "..", "golden", "view_columns.txt")

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, []byte(got.String()), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s (UPDATE_GOLDEN=1)", goldenPath)
		return
	}

	wantRaw, err := os.ReadFile(goldenPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file %s does not exist — create it with:\n  UPDATE_GOLDEN=1 go test -tags duckdb ./internal/aggregate/... -run TestViewColumnsStable", goldenPath)
		}
		t.Fatalf("read golden: %v", err)
	}
	want := string(wantRaw)

	if got.String() != want {
		t.Fatalf(`columns in DuckDB views drifted from %s.

If this was an intentional schema change, update the golden with:
  UPDATE_GOLDEN=1 go test -tags duckdb ./internal/aggregate/... -run TestViewColumnsStable

And update the Python side accordingly:
  - tools/analyze/src/analyze/db.py SELECT statements
  - tools/analyze/tests/conftest.py SCHEMA_DDL mirror view
  - tools/analyze/tests/test_schema_drift.py expected-column list

Otherwise revert the schema change — the Python analyzer references these columns by name and will break at runtime.

--- want ---
%s
--- got ---
%s`, goldenPath, want, got.String())
	}
}

// viewColumns returns the ordered list of column names in view (or
// table) v, using PRAGMA table_info which works the same for both.
func viewColumns(db *sql.DB, v string) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info('%s')", v))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	// PRAGMA table_info returns: cid, name, type, notnull, dflt_value, pk.
	// We only care about `name`. Scan into a slice of any so schema
	// evolution on the PRAGMA side doesn't break us.
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		// `name` is the second column (index 1).
		name, ok := vals[1].(string)
		if !ok {
			return nil, fmt.Errorf("unexpected name type %T", vals[1])
		}
		out = append(out, name)
	}
	return out, rows.Err()
}
