//go:build duckdb

package aggregate_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/wentbackward/resolver/internal/aggregate"
)

// scoreCardFixture is a minimal v2.1 scorecard good enough for ingest
// round-trip testing. Keeps us from depending on the report package
// (would drag adapter/runner behind the build tag).
//
// Carries BOTH summary.tiers (tier-keyed, preserved during migration) and
// summary.roles (role-keyed, what v2.1 ingest reads preferentially). The
// two counter blocks are intentionally consistent so the run-level
// totals (correct/partial/...) come out the same either way.
const scoreCardFixture = `{
  "meta": {
    "model": "gresh-test",
    "endpoint": "http://test/v1/chat/completions",
    "timestamp": "2026-04-19T00:00:00.000Z",
    "queryCount": 2,
    "nodeVersion": "go1.24.0"
  },
  "summary": {
    "roles": {
      "agentic-toolcall": {
        "verdict": "PASS",
        "thresholdMet": true,
        "threshold": 0.9,
        "metrics": {"pct": 1.0},
        "scenarioCountExpected": 2,
        "scenarioCountObserved": 2,
        "correct": 2, "partial": 0, "incorrect": 0, "errors": 0, "total": 2
      }
    },
    "timing": { "totalMs": 200, "avgMs": 100, "p50Ms": 100, "p95Ms": 100, "maxMs": 100, "count": 2 }
  },
  "results": [
    { "role": "agentic-toolcall", "id": "R1.1", "query": "q1", "expectedTool": "exec", "score": "correct", "reason": "", "elapsedMs": 100, "toolCalls": [], "content": null },
    { "role": "agentic-toolcall", "id": "R1.2", "query": "q2", "expectedTool": "exec", "score": "correct", "reason": "", "elapsedMs": 100, "toolCalls": [], "content": null }
  ]
}`

// writeRun creates a {dir}/{runId}.json scorecard and {dir}/manifests/{runId}.json
// manifest. Returns runID.
func writeRun(t *testing.T, dir string, manifestJSON string, runConfigYAML string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "manifests"), 0o755); err != nil {
		t.Fatal(err)
	}
	// manifest → extract runId
	var m struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal([]byte(manifestJSON), &m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifests", m.RunID+".json"), []byte(manifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scorecard.json"), []byte(scoreCardFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if runConfigYAML != "" {
		if err := os.WriteFile(filepath.Join(dir, "run-config.yaml"), []byte(runConfigYAML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return m.RunID
}

func TestIngestIdempotent(t *testing.T) {
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "model-A", "virt-A")
	writeRun(t, runDir, v3Manifest("run-001", "agentic-toolcall"), v2Sidecar())

	dbPath := filepath.Join(t.TempDir(), "r.duckdb")
	opts := aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}

	// First ingest
	if err := aggregate.Run(opts); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	c1 := countRows(t, dbPath)

	// Second ingest
	if err := aggregate.Run(opts); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	c2 := countRows(t, dbPath)

	for k, v := range c1 {
		if c2[k] != v {
			t.Errorf("idempotency broken: table %q went from %d → %d rows", k, v, c2[k])
		}
	}
	if c1["runs"] != 1 {
		t.Errorf("expected 1 run, got %d", c1["runs"])
	}
	if c1["queries"] != 2 {
		t.Errorf("expected 2 queries (from fixture), got %d", c1["queries"])
	}
	if c1["run_config"] != 1 {
		t.Errorf("expected 1 run_config (sidecar provided), got %d", c1["run_config"])
	}
}

// TestIngest_RejectsV1 + TestIngest_RejectsV2 lock the v2.1 forward-only
// policy: pre-v3 manifests discovered under the walk roots are skipped
// with ErrUnsupportedSchema surfaced to stderr. The aggregator continues
// past the bad run rather than aborting the whole sweep — that way a
// mixed research/ tree (forbidden, but possible mid-migration) doesn't
// lose the good runs.
func TestIngest_RejectsV1(t *testing.T) {
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "model-B", "virt-B")
	writeRun(t, runDir, v1Manifest("run-v1-001"), "")

	dbPath := filepath.Join(t.TempDir(), "r.duckdb")
	if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}); err != nil {
		t.Fatalf("aggregate.Run returned hard error; want soft skip: %v", err)
	}
	c := countRows(t, dbPath)
	if c["runs"] != 0 {
		t.Errorf("v1 manifest must NOT contribute a runs row; got %d", c["runs"])
	}
}

func TestIngest_RejectsV2(t *testing.T) {
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "model-C", "virt-C")
	writeRun(t, runDir, v2Manifest("run-v2-001", "1"), v2Sidecar())

	dbPath := filepath.Join(t.TempDir(), "r.duckdb")
	if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}); err != nil {
		t.Fatalf("aggregate.Run returned hard error; want soft skip: %v", err)
	}
	c := countRows(t, dbPath)
	if c["runs"] != 0 {
		t.Errorf("v2 manifest must NOT contribute a runs row; got %d", c["runs"])
	}
	if c["role_scorecards"] != 0 {
		t.Errorf("v2 manifest must NOT contribute role_scorecards rows; got %d", c["role_scorecards"])
	}
}

func TestIngestDryRun(t *testing.T) {
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "m", "v")
	writeRun(t, runDir, v3Manifest("run-dry-001", "agentic-toolcall"), v2Sidecar())

	dbPath := filepath.Join(t.TempDir(), "r.duckdb")
	err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath, DryRun: true})
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	// DB should not have been created.
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote DB file at %s; should be no-op", dbPath)
	}
}

func TestIngestCommunityReload(t *testing.T) {
	reportsDir := t.TempDir()
	cbPath := filepath.Join(t.TempDir(), "community.yaml")
	if err := os.WriteFile(cbPath, []byte(`
entries:
  - model: M1
    benchmark: bfcl
    metric: overall
    value: 0.75
    source_url: https://example.com
    as_of: 2026-01-01
  - model: M2
    benchmark: mmlu
    metric: 5shot
    value: 0.85
    source_url: https://example.com
    as_of: 2026-01-01
`), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "r.duckdb")
	if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath, CommunityBenchmarks: cbPath}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	c := countRows(t, dbPath)
	if c["community_benchmarks"] != 2 {
		t.Errorf("community_benchmarks: got %d, want 2", c["community_benchmarks"])
	}
}

func TestIngestComparisonView(t *testing.T) {
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "model-A", "virt-A")
	writeRun(t, runDir, v3Manifest("run-view-001", "agentic-toolcall"), v2Sidecar())

	dbPath := filepath.Join(t.TempDir(), "r.duckdb")
	if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM comparison`).Scan(&n); err != nil {
		t.Fatalf("select comparison: %v", err)
	}
	if n != 2 {
		t.Errorf("comparison view rows: got %d, want 2", n)
	}
}

// ---- helpers ----

func countRows(t *testing.T, dbPath string) map[string]int {
	t.Helper()
	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string]int{}
	for _, tbl := range []string{"runs", "queries", "sweep_rows", "run_config", "role_scorecards", "community_benchmarks"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		out[tbl] = n
	}
	return out
}

// v3Manifest is the v2.1 manifest shape — manifestVersion 3 with a
// non-empty `role` and no `tier`. This is the only shape the aggregator
// accepts; v1 and v2 are kept for rejection tests only.
func v3Manifest(runID, role string) string {
	return `{
  "manifestVersion": 3,
  "runId": "` + runID + `",
  "model": "gresh-test",
  "resolvedRealModel": "TestOrg/TestModel",
  "adapter": "openai-chat",
  "tokenizerMode": "heuristic",
  "endpoint": "http://test/v1/chat/completions",
  "role": "` + role + `",
  "parallel": false,
  "scenarioHashes": {},
  "startedAt": "2026-04-19T00:00:00Z",
  "finishedAt": "2026-04-19T00:00:30Z",
  "goVersion": "go1.24.0",
  "commitSha": "abc"
}`
}

func v2Manifest(runID, tier string) string {
	return `{
  "manifestVersion": 2,
  "runId": "` + runID + `",
  "model": "gresh-test",
  "resolvedRealModel": "TestOrg/TestModel",
  "adapter": "openai-chat",
  "tokenizerMode": "heuristic",
  "endpoint": "http://test/v1/chat/completions",
  "tier": "` + tier + `",
  "parallel": false,
  "scenarioHashes": {},
  "startedAt": "2026-04-19T00:00:00Z",
  "finishedAt": "2026-04-19T00:00:30Z",
  "goVersion": "go1.24.0",
  "commitSha": "abc"
}`
}

func v1Manifest(runID string) string {
	// No `runConfig` or `resolvedRealModel` fields; matches the shape
	// shipped before the v2 cut.
	return `{
  "manifestVersion": 1,
  "runId": "` + runID + `",
  "model": "gresh-legacy",
  "adapter": "openai-chat",
  "tokenizerMode": "heuristic",
  "endpoint": "http://legacy/v1/chat/completions",
  "parallel": false,
  "scenarioHashes": {},
  "startedAt": "2026-03-01T00:00:00Z",
  "finishedAt": "2026-03-01T00:00:20Z",
  "goVersion": "go1.22.0",
  "commitSha": "legacyabc"
}`
}

func v2Sidecar() string {
	return `
virtual_model: gresh-test
real_model: TestOrg/TestModel
backend_port: 3040
default_temperature: 0.7
default_enable_thinking: true
clamp_enable_thinking: true
context_size: 131072
tool_parser: qwen3_xml
mtp: true
mtp_method: qwen3_next_mtp
notes: "test sidecar"
`
}
