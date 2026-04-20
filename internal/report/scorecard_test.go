package report_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/report"
	"github.com/wentbackward/resolver/internal/runner"
	"github.com/wentbackward/resolver/internal/scenario"
	"github.com/wentbackward/resolver/internal/verdict"
)

// TestScorecardShape locks the v2.1 scorecard shape. summary.roles{}
// replaces summary.tiers{} + top-level overall; gated roles carry a
// verdict + thresholdMet + threshold, ungated roles are "INFO" and
// omit those fields. Breaking any of these also breaks cross-model
// historical comparisons and the aggregator's role_scorecards view.
func TestScorecardShape(t *testing.T) {
	meta := report.Meta{
		Model:       "gresh-general",
		Endpoint:    "http://spark-01:4000/v1/chat/completions",
		Timestamp:   scenario.ScorecardTimestamp(time.Date(2026, 4, 2, 14, 34, 56, 464000000, time.UTC)),
		QueryCount:  1,
		NodeVersion: "go1.22.7",
	}
	results := []runner.PerQuery{{
		Role: scenario.RoleAgenticToolcall, ID: "T1.1",
		Query:        "restart the vllm 35b container",
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

	// Summary keys: thresholds, roles, timing. No overall, no tiers.
	summary, _ := decoded["summary"].(map[string]any)
	if _, ok := summary["thresholds"]; !ok {
		t.Error("summary.thresholds missing")
	}
	if _, ok := summary["roles"]; !ok {
		t.Error("summary.roles missing (v2.1 key)")
	}
	if _, ok := summary["timing"]; !ok {
		t.Error("summary.timing missing")
	}
	if _, ok := summary["overall"]; ok {
		t.Error("summary.overall present — v2.1 carries verdict per-role, not as a top-level field")
	}
	if _, ok := summary["tiers"]; ok {
		t.Error("summary.tiers present — v2.1 replaces tiers with roles")
	}

	roles, _ := summary["roles"].(map[string]any)
	// Only the observed role must appear (canonical order is enforced by
	// MarshalJSON; observed-only keeps the golden stable across partial runs).
	if _, ok := roles["agentic-toolcall"]; !ok {
		t.Errorf("summary.roles missing observed role agentic-toolcall, got %v", keysOf(roles))
	}
	if len(roles) != 1 {
		t.Errorf("summary.roles has %d entries; want 1 (observed-only)", len(roles))
	}

	// Role entry shape. Gated role must carry thresholdMet + threshold +
	// verdict of PASS/FAIL; ungated roles would be INFO with no threshold
	// fields. agentic-toolcall is gated at 90% in shared/gate-thresholds.yaml,
	// so the single-correct test above yields pct=100 → PASS.
	rs, _ := roles["agentic-toolcall"].(map[string]any)
	for _, k := range []string{"verdict", "metrics", "scenarios", "correct", "partial", "incorrect", "errors", "total", "pct", "avgMs", "p50Ms", "scenarioCountExpected", "scenarioCountObserved"} {
		if _, ok := rs[k]; !ok {
			t.Errorf("role entry missing key %q", k)
		}
	}

	// Thresholds rows synthesized only for gated roles that have
	// observed scenarios. In this single-role test, exactly one.
	thr, _ := summary["thresholds"].([]any)
	if len(thr) != 1 {
		t.Errorf("summary.thresholds has %d rows, want 1 (only the observed gated role)", len(thr))
	}

	// Per-query shape: tier may be omitted (v2.1 scenarios carry role);
	// role must be present on every result. Tool call shape unchanged.
	results0 := decoded["results"].([]any)[0].(map[string]any)
	if _, ok := results0["role"]; !ok {
		t.Errorf("result missing role; got keys %v", keysOf(results0))
	}
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

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
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
