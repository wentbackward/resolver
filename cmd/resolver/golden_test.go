package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"

	"github.com/wentbackward/resolver/internal/report"
	"github.com/wentbackward/resolver/internal/runner"
	"github.com/wentbackward/resolver/internal/scenario"
)

// v1MigratedRoleDirs is the embedded role-organised path set that
// collectively contains the original 31 Tier 1 scenarios migrated in
// v2.1 Phase 2. The golden replay walks these (not the full roles/
// tree) because canned-responses.json is keyed on the 31 historical
// scenario IDs (T1.1…T10.3). New v2.1-native roles (reducer-json,
// classifier, multiturn, tool-count-survival, long-context,
// reducer-sexp) are deliberately excluded — they have no canned
// entries and would error out.
var v1MigratedRoleDirs = []string{
	"roles/agentic-toolcall",
	"roles/safety-refuse",
	"roles/safety-escalate",
	"roles/health-check",
	"roles/node-resolution",
	"roles/dep-reasoning",
	"roles/hitl",
}

// loadV1MigratedScenarios walks the 7 role dirs holding v1-migrated
// scenarios and returns them sorted by ID so the replay execution order
// matches canned-responses.json regardless of filesystem iteration
// quirks.
func loadV1MigratedScenarios(t *testing.T, ds dataSource) []scenario.Scenario {
	t.Helper()
	var all []scenario.Scenario
	for _, d := range v1MigratedRoleDirs {
		sc, err := ds.walkScenarios(d)
		if err != nil {
			t.Fatalf("walk %s: %v", d, err)
		}
		all = append(all, sc...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	return all
}

// TestGoldenReplay asserts byte-exact parity between the committed
// `golden/scorecard_example.json` and the scorecard produced by replaying
// `golden/canned-responses.json` in-process. This is the v2.1 parity
// anchor: any future code change that drifts the scorecard shape, the
// verdict logic, the timing aggregates, or the role ordering must be
// caught here.
//
// Set UPDATE_GOLDEN=1 to rewrite the golden file in-place (used after
// an intentional shape change).
//
// The test runs fully offline — no network, no subprocess. It exercises:
//   - embedded scenario loading (roles/<v1-migrated>/*.yaml + system-prompt.md + tools)
//   - replay loader (envelope shape with captured meta)
//   - RunTier1 executor (replayer path, not live HTTP)
//   - verdict evaluation for all 31 scenarios + partial-credit rules
//   - report.Build (role aggregation, threshold gating, timing aggregates)
//   - json.MarshalIndent + trailing-newline contract
func TestGoldenReplay(t *testing.T) {
	ds := dataSource{}
	tools, sysPrompt, err := ds.loadToolsAndPrompt()
	if err != nil {
		t.Fatalf("load tools + prompt: %v", err)
	}
	scenarios := loadV1MigratedScenarios(t, ds)
	if len(scenarios) != 31 {
		t.Fatalf("expected 31 v1-migrated scenarios, got %d", len(scenarios))
	}

	rp, capturedMeta, err := loadReplay("../../golden/canned-responses.json")
	if err != nil {
		t.Fatalf("load replay: %v", err)
	}
	if capturedMeta == nil {
		t.Fatalf("replay file must use envelope shape with meta block")
	}

	perQueries := runner.RunTier1(context.Background(), nil, scenarios, runner.ExecuteOpts{
		SystemPrompt: sysPrompt,
		Tools:        tools,
		Model:        capturedMeta.Model,
		Replayer:     rp,
	})

	meta := *capturedMeta
	meta.QueryCount = len(scenarios)
	sc := report.Build(meta, perQueries)

	got, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile("../../golden/scorecard_example.json", got, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
		t.Log("UPDATE_GOLDEN=1: rewrote golden/scorecard_example.json")
		return
	}

	want, err := os.ReadFile("../../golden/scorecard_example.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Logf("got length: %d, want length: %d", len(got), len(want))
		// Write the drift to a tempfile so the failure is investigable.
		tmp, ferr := os.CreateTemp("", "scorecard-drift-*.json")
		if ferr == nil {
			_, _ = tmp.Write(got)
			_ = tmp.Close()
			t.Logf("actual output written to %s for inspection", tmp.Name())
		}
		t.Fatalf("scorecard drifted from golden/scorecard_example.json — any code change that changes this must be a deliberate, documented v2.1-parity break (rerun with UPDATE_GOLDEN=1 after inspection)")
	}
}

// TestGoldenReplayUnderYAMLThresholds re-runs the replay with the
// GatedTiers() source swapped from the hardcoded defaults to the
// embedded v2.1 YAML. The output scorecard must be byte-identical to
// the golden.
//
// This is the "scorecard-byte-parity" gate — it guards against the
// YAML-defaults diverging from the hardcoded fallback in a way that
// would invisibly drift the scorecard shape.
func TestGoldenReplayUnderYAMLThresholds(t *testing.T) {
	dataDir := dataSource{}
	raw, err := dataDir.readFile("shared/gate-thresholds.yaml")
	if err != nil {
		t.Fatalf("read embedded thresholds: %v", err)
	}
	checks, err := scenario.ParseGateThresholdsBytes([]byte(raw))
	if err != nil {
		t.Fatalf("parse embedded thresholds: %v", err)
	}

	// Snapshot + restore the global override around the test.
	scenario.SetGatedTiers(checks)
	t.Cleanup(scenario.ResetGatedTiersToDefaults)

	// Re-run the same replay → render → diff path as TestGoldenReplay.
	tools, sysPrompt, err := dataDir.loadToolsAndPrompt()
	if err != nil {
		t.Fatal(err)
	}
	scenarios := loadV1MigratedScenarios(t, dataDir)
	rp, capturedMeta, err := loadReplay("../../golden/canned-responses.json")
	if err != nil {
		t.Fatal(err)
	}
	perQueries := runner.RunTier1(context.Background(), nil, scenarios, runner.ExecuteOpts{
		SystemPrompt: sysPrompt,
		Tools:        tools,
		Model:        capturedMeta.Model,
		Replayer:     rp,
	})
	meta := *capturedMeta
	meta.QueryCount = len(scenarios)
	sc := report.Build(meta, perQueries)

	got, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile("../../golden/scorecard_example.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("scorecard drift under YAML-driven thresholds — embedded YAML diverged from hardcoded fallback")
	}
}
