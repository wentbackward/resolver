package aggregate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wentbackward/resolver/internal/aggregate"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "community-benchmarks.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCommunityHappyPath(t *testing.T) {
	p := writeYAML(t, `
entries:
  - model: Qwen3.6-35B-A3B-FP8
    benchmark: bfcl
    metric: overall
    value: 0.78
    source_url: https://gorilla.cs.berkeley.edu/leaderboard.html
    as_of: 2026-04-01
  - model: Claude-Sonnet-4.6
    benchmark: mmlu
    metric: 5shot
    value: 0.89
    source_url: https://example.com
    as_of: 2026-03-15
    notes: "pre-release eval"
`)
	got, err := aggregate.LoadCommunity(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0].Value != 0.78 || got[0].Model != "Qwen3.6-35B-A3B-FP8" {
		t.Errorf("round-trip lost data: %+v", got[0])
	}
	if got[1].Notes != "pre-release eval" {
		t.Errorf("notes round-trip: %q", got[1].Notes)
	}
}

func TestLoadCommunityFutureAsOfRejected(t *testing.T) {
	future := time.Now().Add(48 * time.Hour).UTC().Format("2006-01-02")
	p := writeYAML(t, `
entries:
  - model: M
    benchmark: b
    metric: m
    value: 1
    source_url: https://x
    as_of: `+future+`
`)
	_, err := aggregate.LoadCommunity(p)
	if err == nil {
		t.Fatal("expected error for future as_of")
	}
	if !strings.Contains(err.Error(), "future") {
		t.Errorf("expected 'future' in error, got: %v", err)
	}
}

func TestLoadCommunityRequiredFields(t *testing.T) {
	cases := map[string]string{
		"missing model":     `entries: [{benchmark: b, metric: m, value: 1, source_url: https://x, as_of: 2026-01-01}]`,
		"missing benchmark": `entries: [{model: M, metric: m, value: 1, source_url: https://x, as_of: 2026-01-01}]`,
		"missing metric":    `entries: [{model: M, benchmark: b, value: 1, source_url: https://x, as_of: 2026-01-01}]`,
		"missing source":    `entries: [{model: M, benchmark: b, metric: m, value: 1, as_of: 2026-01-01}]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeYAML(t, body)
			if _, err := aggregate.LoadCommunity(p); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestLoadCommunityAsOfFormat(t *testing.T) {
	p := writeYAML(t, `
entries:
  - model: M
    benchmark: b
    metric: m
    value: 1
    source_url: https://x
    as_of: "April 1, 2026"
`)
	_, err := aggregate.LoadCommunity(p)
	if err == nil || !strings.Contains(err.Error(), "YYYY-MM-DD") {
		t.Errorf("expected YYYY-MM-DD error, got: %v", err)
	}
}

func TestLoadCommunityEmptyFile(t *testing.T) {
	p := writeYAML(t, `entries: []`)
	got, err := aggregate.LoadCommunity(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 entries, got %d", len(got))
	}
}

func TestLoadCommunityRejectsOversizedYAML(t *testing.T) {
	// Write a file that exceeds the 1 MB cap.
	dir := t.TempDir()
	p := filepath.Join(dir, "community-benchmarks.yaml")
	// Build a file slightly over 1 MB by writing a large comment header followed
	// by a valid entries block. The content itself is irrelevant — size is all
	// that matters for this guard.
	const oneMB = 1 << 20
	buf := make([]byte, oneMB+1)
	// Fill with '#' so the YAML parser never even sees it (readCapped rejects
	// before Unmarshal).
	for i := range buf {
		buf[i] = '#'
	}
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := aggregate.LoadCommunity(p)
	if err == nil {
		t.Fatal("expected error for oversized YAML, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected 'exceeds' in error message, got: %v", err)
	}
}
