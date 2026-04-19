//go:build duckdb

package aggregate

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
	"github.com/wentbackward/resolver/internal/manifest"
)

// Run walks the report roots, ingests every manifest + sibling scorecard
// (and any run-config.yaml alongside), reloads community_benchmarks from
// the YAML, and writes the resulting rows to DuckDB. Idempotent: rerunning
// against the same inputs is a no-op (upserts by run_id). Tolerant of v1
// manifests (no run_config row written when manifest.runConfig is nil).
func Run(opts Options) error {
	roots := rootsOrDefault(opts.ReportsDir)
	var newRuns []discovered
	for _, root := range roots {
		found, err := walkRoot(root)
		if err != nil {
			return err
		}
		newRuns = append(newRuns, found...)
	}

	var benchmarks []CommunityBenchmark
	if opts.CommunityBenchmarks != "" {
		cb, err := LoadCommunity(opts.CommunityBenchmarks)
		if err != nil {
			return err
		}
		benchmarks = cb
	}

	if opts.DryRun {
		fmt.Printf("aggregate --dry-run: %d manifest(s) across %d root(s)\n", len(newRuns), len(roots))
		for _, r := range newRuns {
			fmt.Printf("  %s  (%s)\n", r.runID, r.manifestPath)
		}
		if len(benchmarks) > 0 {
			fmt.Printf("  + %d community-benchmark row(s) (would truncate-and-reload)\n", len(benchmarks))
		}
		return nil
	}

	dbPath := opts.DBPath
	if dbPath == "" {
		dbPath = "reports/resolver.duckdb"
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return err
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		return fmt.Errorf("open duckdb %s: %w", dbPath, err)
	}
	defer db.Close()

	if err := migrate(db); err != nil {
		return err
	}

	ingested := 0
	for _, r := range newRuns {
		if err := ingestRun(db, r); err != nil {
			fmt.Fprintf(os.Stderr, "warn: ingest %s: %v\n", r.runID, err)
			continue
		}
		ingested++
	}

	if benchmarks != nil {
		if err := reloadCommunity(db, benchmarks); err != nil {
			return fmt.Errorf("community reload: %w", err)
		}
	}

	if err := refreshViews(db); err != nil {
		return err
	}

	fmt.Printf("aggregate: ingested %d run(s) into %s\n", ingested, dbPath)
	if len(benchmarks) > 0 {
		fmt.Printf("            reloaded %d community-benchmark row(s)\n", len(benchmarks))
	}
	return nil
}

func rootsOrDefault(r string) []string {
	if r == "" {
		return []string{"reports", "research/captures"}
	}
	return strings.Split(r, ",")
}

// discovered pairs a manifest with its sibling scorecard and optional
// run-config sidecar. All paths are absolute or repo-relative.
type discovered struct {
	runID          string
	manifestPath   string
	scorecardPath  string
	runConfigPath  string
}

// walkRoot discovers all manifest.json files under root. For each
// manifest, it looks for the accompanying scorecard + run-config in the
// manifest's grandparent directory (manifests/{runId}.json → parent
// is manifests/; grandparent is the run dir).
func walkRoot(root string) ([]discovered, error) {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	}
	var out []discovered
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(p, ".json") {
			return nil
		}
		if filepath.Base(filepath.Dir(p)) != "manifests" {
			return nil
		}
		runID := strings.TrimSuffix(filepath.Base(p), ".json")
		runDir := filepath.Dir(filepath.Dir(p)) // strip /manifests/
		scorecard, err := findScorecard(runDir, runID)
		if err != nil {
			// Sweep runs emit CSV, not a JSON scorecard — silently skip.
			// For non-sweep runs, warn so the operator knows something is
			// off without failing the whole ingest.
			if !isSweepManifest(p) {
				fmt.Fprintf(os.Stderr, "warn: manifest %s has no sibling scorecard under %s: %v\n", p, runDir, err)
			}
			return nil
		}
		rc := filepath.Join(runDir, "run-config.yaml")
		if _, err := os.Stat(rc); err != nil {
			rc = "" // optional
		}
		out = append(out, discovered{
			runID:         runID,
			manifestPath:  p,
			scorecardPath: scorecard,
			runConfigPath: rc,
		})
		return nil
	})
	return out, err
}

// isSweepManifest reads just enough of a manifest to check whether it was
// written by a sweep run (in which case the sibling is a CSV, not a JSON
// scorecard, and our current aggregator path skips it).
func isSweepManifest(p string) bool {
	raw, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var peek struct {
		Sweep string `json:"sweep"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return false
	}
	return peek.Sweep != ""
}

// findScorecard returns the scorecard JSON path in runDir. If there are
// multiple (legacy flat layouts, pre-rename), picks the one whose
// timestamp most closely matches runID's embedded timestamp.
func findScorecard(runDir, runID string) (string, error) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return "", err
	}
	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		candidates = append(candidates, filepath.Join(runDir, e.Name()))
	}
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no scorecard JSON in %s", runDir)
	case 1:
		return candidates[0], nil
	default:
		// Sort lexicographically and return the one whose name contains
		// the closest timestamp to runID's ts prefix. Simpler fallback:
		// sort descending, return first.
		sort.Strings(candidates)
		// Try to find scorecard.json by convention (new layout).
		for _, c := range candidates {
			if filepath.Base(c) == "scorecard.json" {
				return c, nil
			}
		}
		return candidates[len(candidates)-1], nil
	}
}

// ingestRun upserts one run's manifest + scorecard + optional run-config
// into the DB inside a single transaction.
func ingestRun(db *sql.DB, r discovered) error {
	manifestRaw, err := os.ReadFile(r.manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if m.ManifestVersion > manifest.SchemaVersion {
		fmt.Fprintf(os.Stderr, "warn: manifest %s reports version %d (harness ships %d); ingesting best-effort\n",
			r.manifestPath, m.ManifestVersion, manifest.SchemaVersion)
	}

	scorecardRaw, err := os.ReadFile(r.scorecardPath)
	if err != nil {
		return fmt.Errorf("read scorecard: %w", err)
	}
	var sc scorecardShape
	if err := json.Unmarshal(scorecardRaw, &sc); err != nil {
		return fmt.Errorf("parse scorecard: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Upsert runs row.
	var correct, partial, incorrect, errs int
	for _, t := range sc.Summary.Tiers {
		correct += t.Correct
		partial += t.Partial
		incorrect += t.Incorrect
		errs += t.Errors
	}
	_, err = tx.Exec(`DELETE FROM runs WHERE run_id = ?`, r.runID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO runs (
		run_id, scorecard_path, manifest_path,
		tier, sweep, model, resolved_real_model, endpoint, adapter, tokenizer_mode,
		manifest_version, started_at, finished_at, go_version, commit_sha, host_name,
		overall, total_ms, avg_ms, p50_ms, p95_ms, max_ms,
		query_count, correct_count, partial_count, incorrect_count, error_count
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.runID, r.scorecardPath, r.manifestPath,
		m.Tier, m.Sweep, m.Model, m.ResolvedRealModel, m.Endpoint, m.Adapter, m.TokenizerMode,
		m.ManifestVersion, parseTS(m.StartedAt), parseTS(m.FinishedAt), m.GoVersion, m.CommitSHA, m.HostName,
		sc.Summary.Overall, sc.Summary.Timing.TotalMs, sc.Summary.Timing.AvgMs, sc.Summary.Timing.P50Ms, sc.Summary.Timing.P95Ms, sc.Summary.Timing.MaxMs,
		sc.Meta.QueryCount, correct, partial, incorrect, errs,
	)
	if err != nil {
		return fmt.Errorf("insert runs: %w", err)
	}

	// queries: delete + reinsert (simpler than tracking per-query identity).
	if _, err := tx.Exec(`DELETE FROM queries WHERE run_id = ?`, r.runID); err != nil {
		return err
	}
	for _, q := range sc.Results {
		tcJSON, _ := json.Marshal(q.ToolCalls)
		var content string
		if q.Content != nil {
			if s, ok := q.Content.(string); ok {
				content = s
			}
		}
		if _, err := tx.Exec(`INSERT INTO queries (
			run_id, tier, scenario_id, query, expected_tool, score, reason,
			elapsed_ms, tool_calls_json, content
		) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			r.runID, q.Tier, q.ID, q.Query, q.ExpectedTool, q.Score, q.Reason,
			q.ElapsedMs, string(tcJSON), content,
		); err != nil {
			return fmt.Errorf("insert queries: %w", err)
		}
	}

	// run_config: only if present on the manifest OR as sidecar YAML.
	rc := m.RunConfig
	if rc == nil && r.runConfigPath != "" {
		parsed, err := manifest.LoadSidecar(r.runConfigPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warn: load sidecar %s: %v\n", r.runConfigPath, err)
		} else {
			rc = parsed
		}
	}
	if rc != nil {
		if _, err := tx.Exec(`DELETE FROM run_config WHERE run_id = ?`, r.runID); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO run_config (
			run_id, virtual_model, real_model, backend, backend_port,
			default_temperature, default_top_p, default_top_k, default_presence_penalty, default_frequency_penalty,
			default_max_tokens, default_enable_thinking, clamp_enable_thinking,
			container, tensor_parallel, gpu_memory_utilization, context_size, max_num_batched_tokens,
			kv_cache_dtype, attention_backend, prefix_caching, enable_auto_tool_choice,
			tool_parser, reasoning_parser, chat_template,
			mtp, mtp_method, mtp_num_speculative_tokens, load_format, quantization,
			captured_at, proxy_recipe_path, vllm_recipe_path, repeat_group, notes
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			r.runID, rc.VirtualModel, rc.RealModel, rc.Backend, nullableInt(rc.BackendPort),
			nullableF(rc.DefaultTemperature), nullableF(rc.DefaultTopP), nullableI(rc.DefaultTopK),
			nullableF(rc.DefaultPresencePenalty), nullableF(rc.DefaultFrequencyPenalty),
			nullableI(rc.DefaultMaxTokens), nullableB(rc.DefaultEnableThinking), nullableB(rc.ClampEnableThinking),
			rc.Container, nullableInt(rc.TensorParallel), nullableF(rc.GPUMemoryUtilization),
			nullableInt(rc.ContextSize), nullableInt(rc.MaxNumBatchedTokens),
			rc.KVCacheDtype, rc.AttentionBackend, nullableB(rc.PrefixCaching), nullableB(rc.EnableAutoToolChoice),
			rc.ToolParser, rc.ReasoningParser, rc.ChatTemplate,
			nullableB(rc.MTP), rc.MTPMethod, nullableInt(rc.MTPNumSpeculativeTokens), rc.LoadFormat, rc.Quantization,
			rc.CapturedAt, rc.ProxyRecipePath, rc.VLLMRecipePath, rc.RepeatGroup, rc.Notes,
		); err != nil {
			return fmt.Errorf("insert run_config: %w", err)
		}
	}

	return tx.Commit()
}

func reloadCommunity(db *sql.DB, rows []CommunityBenchmark) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM community_benchmarks`); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO community_benchmarks
			(model, benchmark, metric, value, source_url, as_of, notes)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			r.Model, r.Benchmark, r.Metric, r.Value, r.SourceURL, r.AsOf, r.Notes,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func migrate(db *sql.DB) error {
	for _, stmt := range ddl {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("ddl: %w; stmt=%s", err, firstLine(stmt))
		}
	}
	// Record schema version.
	if _, err := db.Exec(`DELETE FROM _meta`); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT INTO _meta (schema_version, updated_at) VALUES (?, ?)`,
		schemaVersion, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func refreshViews(db *sql.DB) error {
	for _, v := range viewDDL {
		if _, err := db.Exec(v); err != nil {
			return fmt.Errorf("view: %w; stmt=%s", err, firstLine(v))
		}
	}
	return nil
}

// ---- scorecard shape mirror ----
// Kept local so the aggregate package doesn't import report (which would
// pull in runner → adapter and drag more surface area behind the tag).

type scorecardShape struct {
	Meta struct {
		Model       string `json:"model"`
		Endpoint    string `json:"endpoint"`
		Timestamp   string `json:"timestamp"`
		QueryCount  int    `json:"queryCount"`
		NodeVersion string `json:"nodeVersion"`
	} `json:"meta"`
	Summary struct {
		Overall string `json:"overall"`
		Tiers   map[string]struct {
			Correct, Partial, Incorrect, Errors, Total, Pct int
		} `json:"tiers"`
		Timing struct {
			TotalMs, AvgMs, P50Ms, P95Ms, MaxMs int64
			Count                               int
		} `json:"timing"`
	} `json:"summary"`
	Results []struct {
		Tier         string `json:"tier"`
		ID           string `json:"id"`
		Query        string `json:"query"`
		ExpectedTool string `json:"expectedTool"`
		Score        string `json:"score"`
		Reason       string `json:"reason"`
		ElapsedMs    int64  `json:"elapsedMs"`
		ToolCalls    []any  `json:"toolCalls"`
		Content      any    `json:"content"`
	} `json:"results"`
}

// ---- helpers ----

func parseTS(s string) any {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return t
}

func nullableF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableI(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableB(p *bool) any {
	if p == nil {
		return nil
	}
	return *p
}
func nullableInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func firstLine(s string) string {
	for i, c := range s {
		if c == '\n' {
			return s[:i]
		}
	}
	return s
}
