package gate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gresham/resolver/internal/gate"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGateLoadAndEval(t *testing.T) {
	pol := writeTemp(t, "policy.yaml", `
description: test policy
rules:
  - label: accuracy must be at least 0.8 at low tool counts
    metric: accuracy
    operator: ">="
    threshold: 0.8
    aggregate: mean
    axis_filter: { axis: tool_count, le: 20 }
  - label: hallucinations must be zero
    metric: hallucinated_tool_count
    operator: "=="
    threshold: 0
    aggregate: max
`)
	p, err := gate.Load(pol)
	if err != nil {
		t.Fatal(err)
	}
	rows := []gate.Row{
		{"tool_count": 5, "accuracy": 1.0, "hallucinated_tool_count": 0},
		{"tool_count": 20, "accuracy": 0.8, "hallucinated_tool_count": 0},
		{"tool_count": 50, "accuracy": 0.4, "hallucinated_tool_count": 0},
	}
	r := gate.Evaluate(p, rows)
	if !r.OverallPass {
		t.Errorf("expected PASS; got %+v", r.Verdicts)
	}

	// Flip a row to fail the first rule.
	rows[1]["accuracy"] = 0.5
	r = gate.Evaluate(p, rows)
	if r.OverallPass {
		t.Errorf("expected FAIL when accuracy drops; got %+v", r.Verdicts)
	}

	// Introduce a hallucination — max rule must fail.
	rows[1]["accuracy"] = 1.0
	rows[0]["hallucinated_tool_count"] = 1
	r = gate.Evaluate(p, rows)
	if r.OverallPass {
		t.Errorf("expected FAIL when hallucinations > 0")
	}
}

func TestGateMissingPolicy(t *testing.T) {
	_, err := gate.Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing policy file")
	}
}
