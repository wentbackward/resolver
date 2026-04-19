//go:build duckdb

package aggregate_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wentbackward/resolver/internal/aggregate"
	"github.com/wentbackward/resolver/internal/manifest"
)

func futureManifest(runID string) string {
	future := strconv.Itoa(manifest.SchemaVersion + 1)
	return `{
  "manifestVersion": ` + future + `,
  "runId": "` + runID + `",
  "model": "gresh-future",
  "resolvedRealModel": "FutureOrg/FutureModel",
  "adapter": "openai-chat",
  "tokenizerMode": "heuristic",
  "endpoint": "http://future/v1/chat/completions",
  "tier": "1",
  "parallel": false,
  "scenarioHashes": {},
  "startedAt": "2026-04-19T00:00:00Z",
  "finishedAt": "2026-04-19T00:00:30Z",
  "goVersion": "go1.24.0",
  "commitSha": "futuresha"
}`
}

// TestIngestForwardCompatWarning verifies that ingesting a manifest whose
// manifestVersion exceeds manifest.SchemaVersion:
//   - emits exactly one "warn: manifest … reports version …" line to stderr
//   - includes the manifest path and the future version number in that line
//   - does NOT return an error (best-effort ingest)
//   - actually writes a run row to the DB
func TestIngestForwardCompatWarning(t *testing.T) {
	// --- set up synthetic run directory ---
	reportsDir := t.TempDir()
	runDir := filepath.Join(reportsDir, "model-future", "virt-future")
	runID := "run-future-001"
	writeRun(t, runDir, futureManifest(runID), "") // no sidecar needed

	// --- capture stderr via os.Pipe ---
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	// --- run ingest ---
	dbPath := filepath.Join(t.TempDir(), "future.duckdb")
	ingestErr := aggregate.Run(aggregate.Options{ReportsDir: reportsDir, DBPath: dbPath})

	// Close write end so the reader drains completely.
	w.Close()

	// Drain captured stderr.
	var stderrLines []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		stderrLines = append(stderrLines, scanner.Text())
	}
	r.Close()

	if ingestErr != nil {
		t.Fatalf("Run() returned error: %v", ingestErr)
	}

	expectedVersion := strconv.Itoa(manifest.SchemaVersion + 1)
	var warnLines []string
	for _, line := range stderrLines {
		if strings.Contains(line, "reports version") {
			warnLines = append(warnLines, line)
		}
	}
	if len(warnLines) != 1 {
		t.Errorf("want exactly 1 forward-compat warning line, got %d: %v", len(warnLines), warnLines)
	}

	if len(warnLines) == 1 {
		wl := warnLines[0]
		wantVersion := "version " + expectedVersion
		if !strings.Contains(wl, wantVersion) {
			t.Errorf("warning line missing %q: %s", wantVersion, wl)
		}
		if !strings.Contains(wl, runID) {
			t.Errorf("warning line missing run-id %q: %s", runID, wl)
		}
	}

	c := countRows(t, dbPath)
	if c["runs"] != 1 {
		t.Errorf("runs: got %d, want 1", c["runs"])
	}
	if c["queries"] != 2 {
		t.Errorf("queries: got %d, want 2 (from scorecard fixture)", c["queries"])
	}
}
