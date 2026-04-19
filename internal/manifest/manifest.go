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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// SchemaVersion tracks breaking changes to the manifest shape. Bump when a
// downstream consumer would have to care.
const SchemaVersion = 1

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
	Sweep             string            `json:"sweep,omitempty"`
	Seeds             []int             `json:"seeds,omitempty"`
	Parallel          bool              `json:"parallel"`
	ScenarioHashes    map[string]string `json:"scenarioHashes"`
	StartedAt         string            `json:"startedAt"`
	FinishedAt        string            `json:"finishedAt"`
	GoVersion         string            `json:"goVersion"`
	CommitSHA         string            `json:"commitSha"`
	HostName          string            `json:"hostName,omitempty"`
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

// WithTier annotates which tier was run.
func (b *Builder) WithTier(tier string) *Builder { b.m.Tier = tier; return b }

// WithSweep annotates which sweep was run.
func (b *Builder) WithSweep(name string, seeds []int, parallel bool) *Builder {
	b.m.Sweep = name
	b.m.Seeds = seeds
	b.m.Parallel = parallel
	return b
}

// WithResolvedRealModel records llm-proxy's backing model when discoverable.
func (b *Builder) WithResolvedRealModel(name string) *Builder {
	b.m.ResolvedRealModel = name
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
	// Deterministic key ordering for scenarioHashes is already handled by
	// encoding/json on maps (sorts keys); explicit sort guarantees the
	// visible slice in tests.
	sort.Strings(keysOf(b.m.ScenarioHashes))

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

func keysOf[K comparable, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
