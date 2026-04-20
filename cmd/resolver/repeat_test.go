package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestTier1RepeatRun exercises the -n N reproducibility path: running
// NOTE: skipped in v2.1 — tier1/ retired (Phase 2 migration); T4 will update.
// Tier 1 three times under --replay must produce three scorecards whose
// `summary` blocks are byte-identical, each with a distinct filename
// (first bare, k>0 suffixed `-repK`), and three manifests sharing a
// single repeat_group value.
func TestTier1RepeatRun(t *testing.T) {
	t.Skip("v2.1 golden regen pending Phase 5 (T4): tier1/ retired")
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

	// Shape assertion: -rep1 scorecard top-level keys and tier names must
	// match golden/scorecard_example.json so a silent schema regression fails CI.
	var rep1Path string
	for _, p := range scorecards {
		if strings.HasSuffix(p, "-rep1.json") {
			rep1Path = p
			break
		}
	}
	goldenPath := filepath.Join(repoRoot(t), "golden", "scorecard_example.json")
	rep1Raw, err := os.ReadFile(rep1Path)
	if err != nil {
		t.Fatalf("read rep1 scorecard: %v", err)
	}
	goldenRaw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden scorecard: %v", err)
	}
	var rep1SC, goldenSC map[string]any
	if err := json.Unmarshal(rep1Raw, &rep1SC); err != nil {
		t.Fatalf("parse rep1 scorecard: %v", err)
	}
	if err := json.Unmarshal(goldenRaw, &goldenSC); err != nil {
		t.Fatalf("parse golden scorecard: %v", err)
	}
	topKeys := func(m map[string]any) []string {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	rep1Keys := topKeys(rep1SC)
	goldenKeys := topKeys(goldenSC)
	if !reflect.DeepEqual(rep1Keys, goldenKeys) {
		t.Fatalf("scorecard top-level keys drifted: got %v want %v", rep1Keys, goldenKeys)
	}
	// Also compare tier names nested under summary.tiers.
	tierNames := func(sc map[string]any) []string {
		summary, ok := sc["summary"].(map[string]any)
		if !ok {
			return nil
		}
		tiers, ok := summary["tiers"].(map[string]any)
		if !ok {
			return nil
		}
		names := make([]string, 0, len(tiers))
		for k := range tiers {
			names = append(names, k)
		}
		sort.Strings(names)
		return names
	}
	rep1Tiers := tierNames(rep1SC)
	goldenTiers := tierNames(goldenSC)
	if rep1Tiers != nil && goldenTiers != nil && !reflect.DeepEqual(rep1Tiers, goldenTiers) {
		t.Fatalf("scorecard tier names drifted: got %v want %v", rep1Tiers, goldenTiers)
	}
}

// repoRoot returns the repository root relative to the current test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	// cmd/resolver → repo root
	return filepath.Dir(filepath.Dir(wd))
}
