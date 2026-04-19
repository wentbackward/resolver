package report_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gresham/resolver/internal/adapter"
	"github.com/gresham/resolver/internal/report"
	"github.com/gresham/resolver/internal/runner"
	"github.com/gresham/resolver/internal/scenario"
	"github.com/gresham/resolver/internal/verdict"
)

// TestScorecardShape locks the spec §7 shape. If this breaks, cross-model
// historical comparisons also break — that's the whole point.
func TestScorecardShape(t *testing.T) {
	meta := report.Meta{
		Model:       "gresh-general",
		Endpoint:    "http://spark-01:4000/v1/chat/completions",
		Timestamp:   scenario.ScorecardTimestamp(time.Date(2026, 4, 2, 14, 34, 56, 464000000, time.UTC)),
		QueryCount:  1,
		NodeVersion: "go1.22.7",
	}
	results := []runner.PerQuery{{
		Tier: scenario.TierT1, ID: "T1.1", Query: "restart the vllm 35b container",
		ExpectedTool: "exec",
		Score:        verdict.ScoreCorrect,
		Reason:       "correct restart command on spark-01",
		ElapsedMs:    2047,
		ToolCalls: []adapter.ToolCall{{Name: "exec", Arguments: map[string]any{
			"node":    "spark-01",
			"command": "docker restart vllm-35b",
		}}},
		Content: nil,
	}}
	sc := report.Build(meta, results)

	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}

	// Meta keys must match spec §7 exactly.
	metaKeys := setOf(t, decoded, "meta")
	for _, k := range []string{"model", "endpoint", "timestamp", "queryCount", "nodeVersion"} {
		if !metaKeys[k] {
			t.Errorf("meta missing key %q — spec §7 parity broken", k)
		}
	}
	if extra := extraKeys(metaKeys, "model", "endpoint", "timestamp", "queryCount", "nodeVersion"); len(extra) > 0 {
		t.Errorf("meta has extra keys %v — move to manifest.json instead", extra)
	}

	// Summary keys.
	summary, _ := decoded["summary"].(map[string]any)
	if _, ok := summary["thresholds"]; !ok {
		t.Error("summary.thresholds missing")
	}
	tiers, ok := summary["tiers"].(map[string]any)
	if !ok {
		t.Fatalf("summary.tiers missing or wrong shape")
	}
	// Every tier T1-T10 must be present, even informational ones.
	for _, tid := range []string{"T1", "T2", "T3", "T4", "T5", "T6", "T7", "T8", "T9", "T10"} {
		if _, ok := tiers[tid]; !ok {
			t.Errorf("summary.tiers missing %s (informational tiers must still appear)", tid)
		}
	}

	// Five gated threshold rows.
	thr, _ := summary["thresholds"].([]any)
	if len(thr) != 5 {
		t.Errorf("summary.thresholds has %d rows, want 5", len(thr))
	}

	// Per-query tool call shape: name + arguments only.
	results0 := decoded["results"].([]any)[0].(map[string]any)
	tc := results0["toolCalls"].([]any)[0].(map[string]any)
	tcKeys := setOfMap(tc)
	for _, k := range []string{"name", "arguments"} {
		if !tcKeys[k] {
			t.Errorf("toolCall missing required key %q", k)
		}
	}
	if extra := extraKeys(tcKeys, "name", "arguments"); len(extra) > 0 {
		t.Errorf("toolCall has extra keys %v — spec §7 only has name+arguments", extra)
	}
}

func setOf(t *testing.T, m map[string]any, key string) map[string]bool {
	t.Helper()
	sub, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("%s missing / wrong type", key)
	}
	return setOfMap(sub)
}

func setOfMap(m map[string]any) map[string]bool {
	s := map[string]bool{}
	for k := range m {
		s[k] = true
	}
	return s
}

func extraKeys(got map[string]bool, allowed ...string) []string {
	allow := map[string]bool{}
	for _, a := range allowed {
		allow[a] = true
	}
	var extra []string
	for k := range got {
		if !allow[k] {
			extra = append(extra, k)
		}
	}
	return extra
}
