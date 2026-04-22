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

	"github.com/wentbackward/resolver/internal/scenario"
)

// TestTier1RepeatRun exercises the -n N reproducibility path: running
// the v1-migrated role scenarios three times under --replay must
// produce three scorecards whose `summary` blocks are byte-identical,
// each with a distinct filename (first bare, k>0 suffixed `-repK`),
// and three manifests sharing a single repeat_group value.
//
// NOTE: retains the Tier1 test name for history; in v2.1 the 31
// scenarios live under roles/ and `--tier=1` walks `tier1/` which is
// now empty. We point the harness at a temporary --data-dir-less run
// that uses the v1-migrated role dirs by passing no tier override and
// relying on the flag parser's default.
func TestTier1RepeatRun(t *testing.T) {
	out := t.TempDir()
	// tier=1 under v2.1: walkScenarios("tier1") returns nothing after
	// Phase 2 retirement, so runTier would abort with "no scenarios
	// loaded". Work around by pointing --data-dir at a generated tree
	// that symlinks the 7 v1-migrated role dirs under tier1/.
	dd := makeV2_1TierShim(t)
	f := flags{
		tier:         "1",
		model:        "gresh-general",
		endpoint:     "http://localhost:4000/v1/chat/completions",
		replay:       filepath.Join(repoRoot(t), "golden", "canned-responses.json"),
		out:          out,
		nSeeds:       3,
		dataDir:      dd,
		noJudge: true, // replay test: no live judge needed
	}
	ds, derr := resolveDataDir(dd)
	if derr != nil {
		t.Fatal(derr)
	}
	// Route loadThresholds at the shimmed dir so the embedded YAML path
	// (shared/gate-thresholds.yaml) still resolves.
	if err := loadThresholds("", ds); err != nil {
		t.Fatalf("loadThresholds: %v", err)
	}
	t.Cleanup(scenario.ResetGatedTiersToDefaults)

	if code := runTier(context.Background(), f, ds); code != 0 && code != 1 {
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

	// Shape assertion: -rep1 scorecard top-level keys and role names
	// must match golden/scorecard_example.json so a silent schema
	// regression fails CI.
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
	// Also compare role names nested under summary.roles.
	roleNames := func(sc map[string]any) []string {
		summary, ok := sc["summary"].(map[string]any)
		if !ok {
			return nil
		}
		roles, ok := summary["roles"].(map[string]any)
		if !ok {
			return nil
		}
		names := make([]string, 0, len(roles))
		for k := range roles {
			names = append(names, k)
		}
		sort.Strings(names)
		return names
	}
	rep1Roles := roleNames(rep1SC)
	goldenRoles := roleNames(goldenSC)
	if rep1Roles != nil && goldenRoles != nil && !reflect.DeepEqual(rep1Roles, goldenRoles) {
		t.Fatalf("scorecard role names drifted: got %v want %v", rep1Roles, goldenRoles)
	}
}

// makeV2_1TierShim builds a temp data dir that mirrors
// cmd/resolver/data, but where `tier1/` contains real copies of the
// YAML files from the 7 v1-migrated role directories. This lets the
// --tier=1 CLI path keep working for repeat-run tests without
// reviving tier1/ at the repo level. We copy rather than symlink
// because filepath.WalkDir in scenario.LoadTree does not descend into
// symlinked directories.
func makeV2_1TierShim(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	src := filepath.Join(root, "cmd", "resolver", "data")
	dst := t.TempDir()
	// Top-level: symlink every entry except tier1/, which we rebuild.
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	for _, e := range entries {
		if e.Name() == "tier1" {
			continue
		}
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if err := os.Symlink(from, to); err != nil {
			t.Fatalf("symlink %s -> %s: %v", from, to, err)
		}
	}
	// Rebuild tier1/ as a real directory.
	tierDst := filepath.Join(dst, "tier1")
	if err := os.Mkdir(tierDst, 0o755); err != nil {
		t.Fatal(err)
	}
	// Carry over tier1-scoped assets (system-prompt.md, gate-thresholds.yaml).
	for _, f := range []string{"system-prompt.md", "gate-thresholds.yaml"} {
		from := filepath.Join(src, "tier1", f)
		if _, err := os.Stat(from); err == nil {
			if err := os.Symlink(from, filepath.Join(tierDst, f)); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Copy each v1-migrated role dir into tier1/. Copying (not
	// symlinking) ensures filepath.WalkDir descends into them.
	for _, rd := range []string{"agentic-toolcall", "safety-refuse", "safety-escalate", "health-check", "node-resolution", "dep-reasoning", "hitl"} {
		if err := copyDir(filepath.Join(src, "roles", rd), filepath.Join(tierDst, rd)); err != nil {
			t.Fatalf("copy %s: %v", rd, err)
		}
	}
	return dst
}

// copyDir recursively copies src into dst (creating dst). Preserves
// file bytes; does not preserve mode. Used only by the shim above.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(from, to); err != nil {
				return err
			}
			continue
		}
		b, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		if err := os.WriteFile(to, b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// repoRoot returns the repository root relative to the current test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	// cmd/resolver → repo root
	return filepath.Dir(filepath.Dir(wd))
}
