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

// TestE2EProvenance is the v2 plan Phase 7 provenance gate: a fully-
// populated --run-config sidecar must round-trip through manifest →
// aggregator → DuckDB `run_config` table with every field intact.
//
// This is the integration test that proves the four v2 pieces
// (manifest v2 + aggregator + community-benchmarks + analyzer) are
// actually wired up as a chain, rather than as isolated units. Every
// field the Phase 1 spec calls out must land as queryable data.
func TestE2EProvenance(t *testing.T) {
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "Qwen_Qwen3.6-35B-A3B-FP8", "gresh-general")

	// Build a fully-populated manifest: every RunConfig field of every
	// category (proxy, engine, capture meta). A field we forget to set
	// here is a field the aggregator won't surface to the analyzer.
	const runID = "e2e-provenance-001"
	manifestJSON := `{
  "manifestVersion": 3,
  "runId": "` + runID + `",
  "model": "gresh-general",
  "resolvedRealModel": "Qwen/Qwen3.6-35B-A3B-FP8",
  "adapter": "openai-chat",
  "tokenizerMode": "heuristic",
  "endpoint": "http://localhost:4000/v1/chat/completions",
  "role": "agentic-toolcall",
  "parallel": false,
  "scenarioHashes": {"T1.1":"abc"},
  "startedAt":  "2026-04-19T00:00:00Z",
  "finishedAt": "2026-04-19T00:01:00Z",
  "goVersion": "go1.24.0",
  "commitSha": "e2etest",
  "runConfig": {
    "virtual_model": "gresh-general",
    "real_model":    "Qwen/Qwen3.6-35B-A3B-FP8",
    "backend_port":  3040,
    "default_temperature":      0.7,
    "default_top_p":            0.95,
    "default_max_tokens":       16384,
    "default_enable_thinking":  true,
    "clamp_enable_thinking":    true,
    "container":             "vllm-node-tf5",
    "tensor_parallel":       1,
    "gpu_memory_utilization": 0.40,
    "context_size":          131072,
    "max_num_batched_tokens": 16384,
    "kv_cache_dtype":        "fp8",
    "attention_backend":     "flashinfer",
    "prefix_caching":        true,
    "enable_auto_tool_choice": true,
    "tool_parser":           "qwen3_xml",
    "reasoning_parser":      "qwen3",
    "chat_template":         "unsloth.jinja",
    "mtp":                   true,
    "mtp_method":            "qwen3_next_mtp",
    "mtp_num_speculative_tokens": 1,
    "load_format":           "fastsafetensors",
    "quantization":          "fp8",
    "captured_at":           "2026-04-19",
    "proxy_recipe_path":     "/srv/llm-proxy/config.yaml@abc123",
    "vllm_recipe_path":      "/srv/vllm-recipes/qwen3.6.yaml@def456",
    "repeat_group":          "e2e-provenance-001",
    "notes":                 "end-to-end provenance smoke"
  }
}`
	writeRun(t, runDir, manifestJSON, "")

	dbPath := filepath.Join(t.TempDir(), "e2e.duckdb")
	if err := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath}); err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	db, err := sql.Open("duckdb", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Pull every RunConfig column back out by name — this is what the
	// Python analyzer eventually sees through the `run_summary` and
	// `comparison` views.
	row := db.QueryRow(`
		SELECT virtual_model, real_model, backend_port,
		       default_temperature, default_top_p, default_max_tokens,
		       default_enable_thinking, clamp_enable_thinking,
		       container, tensor_parallel, gpu_memory_utilization,
		       context_size, max_num_batched_tokens,
		       kv_cache_dtype, attention_backend,
		       prefix_caching, enable_auto_tool_choice,
		       tool_parser, reasoning_parser, chat_template,
		       mtp, mtp_method, mtp_num_speculative_tokens,
		       load_format, quantization,
		       captured_at, proxy_recipe_path, vllm_recipe_path,
		       repeat_group, notes
		FROM run_config WHERE run_id = ?
	`, runID)

	var (
		virtualModel, realModel        string
		backendPort                    int
		defaultTemp, defaultTopP       sql.NullFloat64
		defaultMaxTokens               sql.NullInt64
		defaultEnableThinking          sql.NullBool
		clampEnableThinking            sql.NullBool
		container                      string
		tensorParallel                 sql.NullInt64
		gpuMemUtil                     sql.NullFloat64
		contextSize, maxBatchedTokens  sql.NullInt64
		kvCache, attentionBackend      string
		prefixCaching, enableAutoTool  sql.NullBool
		toolParser, reasoningParser    string
		chatTemplate                   string
		mtp                            sql.NullBool
		mtpMethod                      string
		mtpNumSpec                     sql.NullInt64
		loadFormat, quantization       string
		capturedAt, proxyRecipe        string
		vllmRecipe, repeatGroup, notes string
	)
	if err := row.Scan(
		&virtualModel, &realModel, &backendPort,
		&defaultTemp, &defaultTopP, &defaultMaxTokens,
		&defaultEnableThinking, &clampEnableThinking,
		&container, &tensorParallel, &gpuMemUtil,
		&contextSize, &maxBatchedTokens,
		&kvCache, &attentionBackend,
		&prefixCaching, &enableAutoTool,
		&toolParser, &reasoningParser, &chatTemplate,
		&mtp, &mtpMethod, &mtpNumSpec,
		&loadFormat, &quantization,
		&capturedAt, &proxyRecipe, &vllmRecipe,
		&repeatGroup, &notes,
	); err != nil {
		t.Fatalf("scan run_config: %v", err)
	}

	// Asserts — one per field category. A regression in any column
	// name, type, or round-trip path fires here specifically.
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"virtual_model", virtualModel, "gresh-general"},
		{"real_model", realModel, "Qwen/Qwen3.6-35B-A3B-FP8"},
		{"backend_port", backendPort, 3040},
		{"default_temperature", defaultTemp.Float64, 0.7},
		{"default_top_p", defaultTopP.Float64, 0.95},
		{"default_max_tokens", defaultMaxTokens.Int64, int64(16384)},
		{"default_enable_thinking", defaultEnableThinking.Bool, true},
		{"clamp_enable_thinking", clampEnableThinking.Bool, true},
		{"container", container, "vllm-node-tf5"},
		{"tensor_parallel", tensorParallel.Int64, int64(1)},
		{"gpu_memory_utilization", gpuMemUtil.Float64, 0.40},
		{"context_size", contextSize.Int64, int64(131072)},
		{"max_num_batched_tokens", maxBatchedTokens.Int64, int64(16384)},
		{"kv_cache_dtype", kvCache, "fp8"},
		{"attention_backend", attentionBackend, "flashinfer"},
		{"prefix_caching", prefixCaching.Bool, true},
		{"enable_auto_tool_choice", enableAutoTool.Bool, true},
		{"tool_parser", toolParser, "qwen3_xml"},
		{"reasoning_parser", reasoningParser, "qwen3"},
		{"chat_template", chatTemplate, "unsloth.jinja"},
		{"mtp", mtp.Bool, true},
		{"mtp_method", mtpMethod, "qwen3_next_mtp"},
		{"mtp_num_speculative_tokens", mtpNumSpec.Int64, int64(1)},
		{"load_format", loadFormat, "fastsafetensors"},
		{"quantization", quantization, "fp8"},
		{"captured_at", capturedAt, "2026-04-19"},
		{"proxy_recipe_path", proxyRecipe, "/srv/llm-proxy/config.yaml@abc123"},
		{"vllm_recipe_path", vllmRecipe, "/srv/vllm-recipes/qwen3.6.yaml@def456"},
		{"repeat_group", repeatGroup, "e2e-provenance-001"},
		{"notes", notes, "end-to-end provenance smoke"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v (%T), want %v (%T)", c.name, c.got, c.got, c.want, c.want)
		}
	}

	// comparison view also surfaces a subset of runConfig — sanity-check
	// that the join lands correctly.
	var vRealModel, vToolParser sql.NullString
	var vMTP, vThinking sql.NullBool
	var vContext sql.NullInt64
	if err := db.QueryRow(`
		SELECT cfg_real_model, cfg_thinking, cfg_tool_parser, cfg_mtp, cfg_context_size
		FROM comparison WHERE run_id = ? LIMIT 1
	`, runID).Scan(&vRealModel, &vThinking, &vToolParser, &vMTP, &vContext); err != nil {
		t.Fatalf("select from comparison view: %v", err)
	}
	if vRealModel.String != "Qwen/Qwen3.6-35B-A3B-FP8" || vToolParser.String != "qwen3_xml" ||
		!vMTP.Bool || !vThinking.Bool || vContext.Int64 != 131072 {
		t.Errorf("comparison view didn't surface runConfig: real=%v tp=%v mtp=%v th=%v ctx=%v",
			vRealModel, vToolParser, vMTP, vThinking, vContext)
	}

	// Validate the scorecard JSON is untouched (belt + suspenders — v1
	// parity shouldn't regress just because we added provenance).
	raw, err := os.ReadFile(filepath.Join(runDir, "scorecard.json"))
	if err != nil {
		t.Fatal(err)
	}
	var peek map[string]any
	if err := json.Unmarshal(raw, &peek); err != nil {
		t.Fatal(err)
	}
	if meta, ok := peek["meta"].(map[string]any); ok {
		for _, k := range []string{"model", "endpoint", "timestamp", "queryCount", "nodeVersion"} {
			if _, present := meta[k]; !present {
				t.Errorf("scorecard.meta lost key %q (v1 parity regression)", k)
			}
		}
	} else {
		t.Error("scorecard.meta missing")
	}
}
