package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTier1RepeatRun exercises the -n N reproducibility path: running
// Tier 1 three times under --replay must produce three scorecards whose
// `summary` blocks are byte-identical, each with a distinct filename
// (first bare, k>0 suffixed `-repK`), and three manifests sharing a
// single repeat_group value.
func TestTier1RepeatRun(t *testing.T) {
	out := t.TempDir()
	f := flags{
		tier:      "1",
		model:     "gresh-general",
		endpoint:  "http://localhost:4000/v1/chat/completions",
		replay:    filepath.Join(repoRoot(t), "golden", "canned-responses.json"),
		out:       out,
		nSeeds:    3,
	}
	if code := runTier(context.Background(), f, dataSource{}); code != 0 && code != 1 {
		// code==1 is fine (model under test FAILs the gate); code==2 is a
		// harness error.
		t.Fatalf("runTier exit code %d", code)
	}

	scorecards, err := filepath.Glob(filepath.Join(out, "gresh-general_*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(scorecards) != 3 {
		t.Fatalf("expected 3 scorecards, got %d: %v", len(scorecards), scorecards)
	}

	// At least one scorecard has no suffix, and at least one each of -rep1 / -rep2.
	hasBare, rep1, rep2 := false, false, false
	for _, p := range scorecards {
		switch {
		case strings.HasSuffix(p, "-rep1.json"):
			rep1 = true
		case strings.HasSuffix(p, "-rep2.json"):
			rep2 = true
		default:
			hasBare = true
		}
	}
	if !hasBare || !rep1 || !rep2 {
		t.Errorf("expected one bare + -rep1 + -rep2 scorecard; got %v", scorecards)
	}

	// Summaries byte-identical across the 3 (determinism check).
	var summaryRefs []string
	for _, p := range scorecards {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var obj struct {
			Summary json.RawMessage `json:"summary"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		summaryRefs = append(summaryRefs, string(obj.Summary))
	}
	for i := 1; i < len(summaryRefs); i++ {
		if summaryRefs[i] != summaryRefs[0] {
			t.Errorf("summary drift between scorecard[0] and scorecard[%d] under --replay", i)
		}
	}

	// All 3 manifests share one repeat_group.
	manifests, err := filepath.Glob(filepath.Join(out, "manifests", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 3 {
		t.Fatalf("expected 3 manifests, got %d", len(manifests))
	}
	groups := map[string]struct{}{}
	for _, p := range manifests {
		raw, _ := os.ReadFile(p)
		var m struct {
			RunConfig struct {
				RepeatGroup string `json:"repeat_group"`
			} `json:"runConfig"`
		}
		_ = json.Unmarshal(raw, &m)
		if m.RunConfig.RepeatGroup == "" {
			t.Errorf("manifest %s has no repeat_group", p)
		}
		groups[m.RunConfig.RepeatGroup] = struct{}{}
	}
	if len(groups) != 1 {
		t.Errorf("expected all 3 manifests to share a single repeat_group; got %d distinct: %v", len(groups), groups)
	}
}

// repoRoot returns the repository root relative to the current test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	// cmd/resolver → repo root
	return filepath.Dir(filepath.Dir(wd))
}
