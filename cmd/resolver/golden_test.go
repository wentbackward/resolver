package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/wentbackward/resolver/internal/report"
	"github.com/wentbackward/resolver/internal/runner"
	"github.com/wentbackward/resolver/internal/scenario"
)

// TestGoldenReplay asserts byte-exact parity between the committed
// `golden/scorecard_example.json` and the scorecard produced by replaying
// `golden/canned-responses.json` in-process. This is the v1 parity anchor:
// any future code change that drifts the scorecard shape, the verdict
// logic, the timing aggregates, or the tier ordering must be caught here.
//
// The test runs fully offline — no network, no subprocess. It exercises:
//   - embedded scenario loading (tier1/*.yaml + system-prompt.md + tools)
//   - replay loader (envelope shape with captured meta)
//   - RunTier1 executor (replayer path, not live HTTP)
//   - verdict evaluation for all 31 scenarios + partial-credit rules
//   - report.Build (tier aggregation, threshold gating, timing aggregates)
//   - json.MarshalIndent + trailing-newline contract
func TestGoldenReplay(t *testing.T) {
	ds := dataSource{}
	tools, sysPrompt, err := ds.loadToolsAndPrompt()
	if err != nil {
		t.Fatalf("load tools + prompt: %v", err)
	}
	scenarios, err := ds.walkScenarios("tier1")
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	if len(scenarios) != 31 {
		t.Fatalf("expected 31 scenarios, got %d", len(scenarios))
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
		t.Fatalf("scorecard drifted from golden/scorecard_example.json — any code change that changes this must be a deliberate, documented v1-parity break")
	}
}

// TestGoldenReplayUnderYAMLThresholds re-runs the replay with the
// GatedTiers() source swapped from the hardcoded defaults to the embedded
// YAML. The output scorecard must be byte-identical to the golden.
//
// This is the v2-plan-Phase-3 "scorecard-byte-parity" gate — it guards
// against the YAML-defaults diverging from the hardcoded fallback in a
// way that would invisibly drift the scorecard shape.
func TestGoldenReplayUnderYAMLThresholds(t *testing.T) {
	dataDir := dataSource{}
	raw, err := dataDir.readFile("tier1/gate-thresholds.yaml")
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
	scenarios, err := dataDir.walkScenarios("tier1")
	if err != nil {
		t.Fatal(err)
	}
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
