package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wentbackward/resolver/internal/manifest"
)

// helper
func ptrB(v bool) *bool       { return &v }
func ptrI(v int) *int         { return &v }
func ptrF(v float64) *float64 { return &v }

func TestSchemaVersionBumped(t *testing.T) {
	if manifest.SchemaVersion != 3 {
		t.Errorf("SchemaVersion = %d, want 3 per v2.1 Phase 4", manifest.SchemaVersion)
	}
}

func TestLoadSidecarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "run-config.yaml")
	// Fully-populated sidecar covering every field category
	yamlBody := `
virtual_model: gresh-general
real_model: Qwen/Qwen3.6-35B-A3B-FP8
backend_port: 3040
default_temperature: 0.7
default_top_p: 0.95
default_max_tokens: 16384
default_enable_thinking: true
clamp_enable_thinking: true
container: vllm-node-tf5
tensor_parallel: 1
gpu_memory_utilization: 0.40
context_size: 131072
max_num_batched_tokens: 16384
kv_cache_dtype: fp8
attention_backend: flashinfer
prefix_caching: true
enable_auto_tool_choice: true
tool_parser: qwen3_xml
reasoning_parser: qwen3
chat_template: unsloth.jinja
mtp: true
mtp_method: qwen3_next_mtp
mtp_num_speculative_tokens: 1
load_format: fastsafetensors
quantization: fp8
captured_at: 2026-04-19
notes: "test sidecar"
`
	if err := os.WriteFile(src, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := manifest.LoadSidecar(src)
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}

	// Round-trip check: attach to a manifest, Write it, Read it back, diff.
	b := manifest.NewBuilder("gresh-general", "http://localhost:4000/v1/chat/completions", "openai-chat", "heuristic").
		WithTier("1").WithRunConfig(rc).WithResolvedRealModel("Qwen/Qwen3.6-35B-A3B-FP8")
	path, err := b.Write(dir)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got manifest.Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.ManifestVersion != 3 {
		t.Errorf("ManifestVersion on disk = %d, want 3", got.ManifestVersion)
	}
	if got.RunConfig == nil {
		t.Fatal("runConfig was not serialized to disk")
	}
	if got.RunConfig.RealModel != "Qwen/Qwen3.6-35B-A3B-FP8" {
		t.Errorf("real_model round-trip: got %q", got.RunConfig.RealModel)
	}
	if got.RunConfig.BackendPort == nil || *got.RunConfig.BackendPort != 3040 {
		t.Errorf("backend_port round-trip: got %v", got.RunConfig.BackendPort)
	}
	if got.RunConfig.DefaultEnableThinking == nil || !*got.RunConfig.DefaultEnableThinking {
		t.Errorf("default_enable_thinking did not round-trip")
	}
	if got.RunConfig.ToolParser != "qwen3_xml" {
		t.Errorf("tool_parser round-trip: got %q", got.RunConfig.ToolParser)
	}
	if got.ResolvedRealModel != "Qwen/Qwen3.6-35B-A3B-FP8" {
		t.Errorf("resolvedRealModel: got %q", got.ResolvedRealModel)
	}
}

func TestLoadSidecarMissingFile(t *testing.T) {
	_, err := manifest.LoadSidecar("/does/not/exist.yaml")
	if err == nil {
		t.Fatal("expected error for missing sidecar file")
	}
}

func TestLoadSidecarInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(src, []byte("not: valid:: yaml:"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := manifest.LoadSidecar(src)
	if err == nil {
		t.Fatal("expected yaml parse error")
	}
}

func TestWithoutRunConfigOmitsField(t *testing.T) {
	// A v2 manifest with nil RunConfig must serialize without a `runConfig`
	// key at all (omitempty). Ensures v1-shaped JSON is still producible
	// when no sidecar is supplied.
	dir := t.TempDir()
	b := manifest.NewBuilder("gresh-general", "http://localhost:4000", "openai-chat", "heuristic").WithTier("1")
	path, err := b.Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatal(err)
	}
	if _, present := asMap["runConfig"]; present {
		t.Errorf("runConfig key should be omitted when nil; got full map: %v", asMap)
	}
}

func TestManifest_V3Shape(t *testing.T) {
	dir := t.TempDir()
	b := manifest.NewBuilder("gresh-reasoner", "http://localhost:4000", "openai-chat", "heuristic").
		WithRole("agentic-toolcall").
		WithPromptRev("abc123def456")
	path, err := b.Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatal(err)
	}
	if got, _ := asMap["manifestVersion"].(float64); int(got) != 3 {
		t.Errorf("manifestVersion: got %v, want 3", asMap["manifestVersion"])
	}
	if got, _ := asMap["role"].(string); got != "agentic-toolcall" {
		t.Errorf("role: got %q, want agentic-toolcall", got)
	}
	if got, _ := asMap["promptRev"].(string); got != "abc123def456" {
		t.Errorf("promptRev: got %q, want abc123def456", got)
	}
}

func TestBuilder_WithRole_ProducesNonEmptyRole(t *testing.T) {
	b := manifest.NewBuilder("m", "http://x", "openai-chat", "heuristic").WithRole("reducer-json")
	got := b.Current()
	if got.Role != "reducer-json" {
		t.Errorf("Builder.Current().Role = %q, want reducer-json", got.Role)
	}
}

func TestSidecar_MinPRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "run-config.yaml")
	yamlBody := `
real_model: Qwen/Qwen3.6-35B-A3B-FP8
default_min_p: 0.05
default_temperature: 0.7
`
	if err := os.WriteFile(src, []byte(yamlBody), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, err := manifest.LoadSidecar(src)
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	if rc.DefaultMinP == nil || *rc.DefaultMinP != 0.05 {
		t.Fatalf("default_min_p did not load: got %v", rc.DefaultMinP)
	}

	b := manifest.NewBuilder("gresh-reasoner", "http://localhost:4000", "openai-chat", "heuristic").
		WithRole("agentic-toolcall").WithRunConfig(rc)
	path, err := b.Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got manifest.Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.RunConfig == nil || got.RunConfig.DefaultMinP == nil || *got.RunConfig.DefaultMinP != 0.05 {
		t.Errorf("defaultMinP did not round-trip: got %+v", got.RunConfig)
	}
	// JSON key is defaultMinP (camelCase) per v3 spec.
	var asMap map[string]any
	_ = json.Unmarshal(raw, &asMap)
	rcMap, _ := asMap["runConfig"].(map[string]any)
	if _, present := rcMap["defaultMinP"]; !present {
		t.Errorf("runConfig.defaultMinP key missing in JSON: %v", rcMap)
	}
}

func TestV1ManifestJSONStillReads(t *testing.T) {
	// A v1-shaped manifest on disk (manifestVersion=1, no runConfig field)
	// must unmarshal into the v2 Manifest struct cleanly so the aggregator
	// can ingest historical manifests without error.
	v1 := []byte(`{
  "manifestVersion": 1,
  "runId": "old-run",
  "model": "gresh-general",
  "adapter": "openai-chat",
  "tokenizerMode": "heuristic",
  "endpoint": "http://localhost:4000",
  "scenarioHashes": {},
  "startedAt": "2026-04-01T00:00:00Z",
  "finishedAt": "2026-04-01T00:00:30Z",
  "goVersion": "go1.22.0",
  "commitSha": "abc123"
}`)
	var got manifest.Manifest
	if err := json.Unmarshal(v1, &got); err != nil {
		t.Fatalf("v1 manifest must unmarshal into v2 struct: %v", err)
	}
	if got.ManifestVersion != 1 {
		t.Errorf("ManifestVersion: got %d, want 1", got.ManifestVersion)
	}
	if got.RunConfig != nil {
		t.Errorf("v1 manifest has no runConfig; should be nil, got %+v", got.RunConfig)
	}
	if got.RunID != "old-run" {
		t.Errorf("RunID lost in unmarshal: %q", got.RunID)
	}
}
