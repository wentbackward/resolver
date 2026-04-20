// Package manifest writes the per-run manifest.json sibling to the
// scorecard. This is where Go-specific metadata lives so the scorecard meta
// block can stay byte-identical to spec §7.
package manifest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const maxYAMLBytes = 1 << 20 // 1 MB

func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lr := &io.LimitedReader{R: f, N: limit + 1}
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(buf)) > limit {
		return nil, fmt.Errorf("%s: yaml exceeds %d bytes", path, limit)
	}
	return buf, nil
}

// SchemaVersion tracks breaking changes to the manifest shape. Bump when a
// downstream consumer would have to care. See docs/manifest-schema.md.
//
//   v1: initial release (runId, model, adapter, tokenizerMode, seeds,
//       scenarioHashes, timestamps, goVersion, commitSha, hostName).
//   v2: adds RunConfig sidecar + ResolvedRealModel wired via the
//       ResolveRealModel probe. v1 manifests remain readable by every
//       v2 tool (the aggregator ingests RunConfig as nullable).
//   v3: role-organised test suite. Adds Role + PromptRev (first 12 hex
//       chars of sha256 over the role's composed system prompt) for
//       reproducibility, plus RunConfig.MinP (the gresh-reasoner 0.05
//       clamp that was unrecorded in v2). Tier stays for archival
//       readers but is no longer written by the live harness.
const SchemaVersion = 3

// RunConfig captures the stack configuration behind a given run — both the
// llm-proxy route (clamps, sampling defaults) and the underlying engine
// recipe (vLLM flags, quantization, MTP, parsers). Optional sidecar; every
// field is a pointer so "unset" is distinguishable from "zero".
//
// Any non-local backing (HuggingFace serverless, Anthropic, etc.) must
// record engine-level fields as "unknown" rather than hallucinating —
// per principle #4 of the v2 plan.
type RunConfig struct {
	// Proxy / backing model
	RealModel   string `yaml:"real_model,omitempty" json:"real_model,omitempty"`
	BackendPort *int   `yaml:"backend_port,omitempty" json:"backend_port,omitempty"`
	Backend     string `yaml:"backend,omitempty" json:"backend,omitempty"`

	// Proxy route defaults (harness forces temperature=0 per spec §2; these
	// are recorded for what *would* have applied)
	DefaultTemperature      *float64 `yaml:"default_temperature,omitempty" json:"default_temperature,omitempty"`
	DefaultTopP             *float64 `yaml:"default_top_p,omitempty" json:"default_top_p,omitempty"`
	DefaultTopK             *int     `yaml:"default_top_k,omitempty" json:"default_top_k,omitempty"`
	DefaultMinP             *float64 `yaml:"default_min_p,omitempty" json:"defaultMinP,omitempty"`
	DefaultPresencePenalty  *float64 `yaml:"default_presence_penalty,omitempty" json:"default_presence_penalty,omitempty"`
	DefaultFrequencyPenalty *float64 `yaml:"default_frequency_penalty,omitempty" json:"default_frequency_penalty,omitempty"`
	DefaultMaxTokens        *int     `yaml:"default_max_tokens,omitempty" json:"default_max_tokens,omitempty"`
	DefaultEnableThinking   *bool    `yaml:"default_enable_thinking,omitempty" json:"default_enable_thinking,omitempty"`
	ClampEnableThinking     *bool    `yaml:"clamp_enable_thinking,omitempty" json:"clamp_enable_thinking,omitempty"`

	// Engine (vLLM) layer. Every nullable field is a pointer so that
	// "unset" is distinguishable from a legitimate zero value
	// (tensor_parallel=0 CPU-only, mtp_num_speculative_tokens=0 MTP off,
	// etc.). Principle #4: "unknown" is valid and must not alias to 0.
	Container               string   `yaml:"container,omitempty" json:"container,omitempty"`
	TensorParallel          *int     `yaml:"tensor_parallel,omitempty" json:"tensor_parallel,omitempty"`
	GPUMemoryUtilization    *float64 `yaml:"gpu_memory_utilization,omitempty" json:"gpu_memory_utilization,omitempty"`
	ContextSize             *int     `yaml:"context_size,omitempty" json:"context_size,omitempty"`
	MaxNumBatchedTokens     *int     `yaml:"max_num_batched_tokens,omitempty" json:"max_num_batched_tokens,omitempty"`
	KVCacheDtype            string   `yaml:"kv_cache_dtype,omitempty" json:"kv_cache_dtype,omitempty"`
	AttentionBackend        string   `yaml:"attention_backend,omitempty" json:"attention_backend,omitempty"`
	PrefixCaching           *bool    `yaml:"prefix_caching,omitempty" json:"prefix_caching,omitempty"`
	EnableAutoToolChoice    *bool    `yaml:"enable_auto_tool_choice,omitempty" json:"enable_auto_tool_choice,omitempty"`
	ToolParser              string   `yaml:"tool_parser,omitempty" json:"tool_parser,omitempty"`
	ReasoningParser         string   `yaml:"reasoning_parser,omitempty" json:"reasoning_parser,omitempty"`
	ChatTemplate            string   `yaml:"chat_template,omitempty" json:"chat_template,omitempty"`
	MTP                     *bool    `yaml:"mtp,omitempty" json:"mtp,omitempty"`
	MTPMethod               string   `yaml:"mtp_method,omitempty" json:"mtp_method,omitempty"`
	MTPNumSpeculativeTokens *int     `yaml:"mtp_num_speculative_tokens,omitempty" json:"mtp_num_speculative_tokens,omitempty"`
	LoadFormat              string   `yaml:"load_format,omitempty" json:"load_format,omitempty"`
	Quantization            string   `yaml:"quantization,omitempty" json:"quantization,omitempty"`

	// Capture metadata
	VirtualModel    string `yaml:"virtual_model,omitempty" json:"virtual_model,omitempty"`
	CapturedAt      string `yaml:"captured_at,omitempty" json:"captured_at,omitempty"`
	ProxyRecipePath string `yaml:"proxy_recipe_path,omitempty" json:"proxy_recipe_path,omitempty"`
	VLLMRecipePath  string `yaml:"vllm_recipe_path,omitempty" json:"vllm_recipe_path,omitempty"`
	RepeatGroup     string `yaml:"repeat_group,omitempty" json:"repeat_group,omitempty"`

	// Free-form notes
	Notes string `yaml:"notes,omitempty" json:"notes,omitempty"`
}

// LoadSidecar parses a --run-config YAML file.
func LoadSidecar(path string) (*RunConfig, error) {
	raw, err := readCapped(path, maxYAMLBytes)
	if err != nil {
		return nil, fmt.Errorf("run-config %s: %w", path, err)
	}
	var rc RunConfig
	if err := yaml.Unmarshal(raw, &rc); err != nil {
		return nil, fmt.Errorf("run-config %s yaml: %w", path, err)
	}
	return &rc, nil
}

// Manifest is the per-run reproducibility record.
type Manifest struct {
	ManifestVersion   int               `json:"manifestVersion"`
	RunID             string            `json:"runId"`
	Model             string            `json:"model"`
	ResolvedRealModel string            `json:"resolvedRealModel,omitempty"`
	Adapter           string            `json:"adapter"`
	TokenizerMode     string            `json:"tokenizerMode"`
	Endpoint          string            `json:"endpoint"`
	Tier              string            `json:"tier,omitempty"`
	Role              string            `json:"role,omitempty"`
	PromptRev         string            `json:"promptRev,omitempty"`
	Sweep             string            `json:"sweep,omitempty"`
	Seeds             []int             `json:"seeds,omitempty"`
	Parallel          bool              `json:"parallel"`
	ScenarioHashes    map[string]string `json:"scenarioHashes"`
	StartedAt         string            `json:"startedAt"`
	FinishedAt        string            `json:"finishedAt"`
	GoVersion         string            `json:"goVersion"`
	CommitSHA         string            `json:"commitSha"`
	HostName          string            `json:"hostName,omitempty"`

	// v2: optional sidecar describing the stack behind the run.
	RunConfig *RunConfig `json:"runConfig,omitempty"`
}

// Builder accumulates values during a run, then emits a Manifest.
type Builder struct {
	m Manifest
}

// NewBuilder starts a fresh manifest with sensible defaults.
func NewBuilder(model, endpoint, adapterName, tokenizerMode string) *Builder {
	return &Builder{m: Manifest{
		ManifestVersion: SchemaVersion,
		RunID:           newRunID(time.Now().UTC()),
		Model:           model,
		Adapter:         adapterName,
		TokenizerMode:   tokenizerMode,
		Endpoint:        endpoint,
		ScenarioHashes:  map[string]string{},
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
		GoVersion:       runtime.Version(),
		CommitSHA:       detectCommit(),
	}}
}

// WithTier annotates which tier was run. v2.1+ runs annotate WithRole
// instead; WithTier stays as a no-op-safe helper so archival tests and
// legacy callers keep compiling.
func (b *Builder) WithTier(tier string) *Builder { b.m.Tier = tier; return b }

// WithRole annotates which v2.1 role bucket a run belongs to. Empty
// strings are accepted (and omitted in JSON) so transitional runs
// without a migrated scenario tree still serialise cleanly.
func (b *Builder) WithRole(role string) *Builder { b.m.Role = role; return b }

// WithPromptRev stamps the first 12 hex chars of sha256 over the run's
// composed system prompt (shared preamble + role body). Empty strings
// are accepted so callers that predate the shared-assets step still
// work; in that case the field is omitted from JSON.
func (b *Builder) WithPromptRev(rev string) *Builder { b.m.PromptRev = rev; return b }

// WithSweep annotates which sweep was run.
func (b *Builder) WithSweep(name string, seeds []int, parallel bool) *Builder {
	b.m.Sweep = name
	b.m.Seeds = seeds
	b.m.Parallel = parallel
	return b
}

// WithResolvedRealModel records llm-proxy's backing model when discoverable
// (via the adapter's ResolveRealModel probe). Falls back to "unknown" at the
// caller when the probe fails.
func (b *Builder) WithResolvedRealModel(name string) *Builder {
	b.m.ResolvedRealModel = name
	return b
}

// WithRunConfig attaches a parsed sidecar describing the run's stack state.
func (b *Builder) WithRunConfig(rc *RunConfig) *Builder {
	b.m.RunConfig = rc
	return b
}

// AddScenarioHash records sha256(scenario bytes) for deterministic diffing.
func (b *Builder) AddScenarioHash(id string, data []byte) *Builder {
	sum := sha256.Sum256(data)
	b.m.ScenarioHashes[id] = hex.EncodeToString(sum[:])
	return b
}

// Write finalizes and serializes the manifest to dir/manifests/{runId}.json.
func (b *Builder) Write(dir string) (string, error) {
	b.m.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	if hn, err := os.Hostname(); err == nil {
		b.m.HostName = hn
	}
	outDir := filepath.Join(dir, "manifests")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, b.m.RunID+".json")
	raw, err := json.MarshalIndent(b.m, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Current returns a copy of the in-progress manifest (for logging / tests).
func (b *Builder) Current() Manifest {
	m := b.m
	return m
}

// newRunID builds a ULID-ish identifier: {ts}-{hex8}. Good enough without a
// dep; sortable by time.
func newRunID(t time.Time) string {
	ts := t.UTC().Format("20060102T150405")
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%s", ts, hex.EncodeToString(b[:]))
}

// detectCommit returns the commit sha of the harness, or "unknown". Uses
// `git rev-parse HEAD` against the cwd.
func detectCommit() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
