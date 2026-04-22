package verdict_test

import (
	"testing"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/scenario"
	"github.com/wentbackward/resolver/internal/verdict"
)

func mkScenario(correct, partial []scenario.Matcher) *scenario.Scenario {
	return &scenario.Scenario{
		ID: "t",
		Tier: scenario.TierT1,
		Rule: scenario.Rule{
			CorrectIf: correct,
			PartialIf: partial,
		},
	}
}

func call(name string, args map[string]any) adapter.ToolCall {
	return adapter.ToolCall{Name: name, Arguments: args}
}

func TestToolCallRequired(t *testing.T) {
	s := mkScenario([]scenario.Matcher{
		{ToolCallRequired: &scenario.ToolCallMatch{
			Name: "exec",
			ArgsRegex: map[string]string{
				"node":    "^spark-01$",
				"command": "docker\\s+ps",
			},
		}},
	}, nil)

	good := []adapter.ToolCall{call("exec", map[string]any{"node": "spark-01", "command": "docker ps -a"})}
	bad := []adapter.ToolCall{call("exec", map[string]any{"node": "claw", "command": "docker ps -a"})}

	if got := verdict.Evaluate(s, good, "").Score; got != verdict.ScoreCorrect {
		t.Errorf("good: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, bad, "").Score; got != verdict.ScoreIncorrect {
		t.Errorf("bad: got %s want incorrect", got)
	}
}

func TestPartialFallback(t *testing.T) {
	s := mkScenario(
		[]scenario.Matcher{
			{ToolCallRequired: &scenario.ToolCallMatch{Name: "health_check", ArgsRegex: map[string]string{"service": "vllm-35b"}}},
		},
		[]scenario.Matcher{
			{ToolCallRequired: &scenario.ToolCallMatch{Name: "exec", ArgsRegex: map[string]string{"command": "curl.*3040"}}},
		},
	)
	partial := []adapter.ToolCall{call("exec", map[string]any{"node": "spark-01", "command": "curl http://localhost:3040/health"})}
	if got := verdict.Evaluate(s, partial, "").Score; got != verdict.ScorePartial {
		t.Errorf("got %s want partial", got)
	}
}

func TestCountAtLeast(t *testing.T) {
	s := mkScenario([]scenario.Matcher{
		{ToolCallCountAtLeast: &scenario.ToolCallCount{Name: "health_check", Min: 3}},
	}, []scenario.Matcher{
		{ToolCallCountInRange: &scenario.ToolCallCountRange{Name: "health_check", Min: 1, Max: 2}},
	})
	three := []adapter.ToolCall{
		call("health_check", map[string]any{"node": "spark-01", "service": "a"}),
		call("health_check", map[string]any{"node": "spark-01", "service": "b"}),
		call("health_check", map[string]any{"node": "spark-01", "service": "c"}),
	}
	two := three[:2]
	zero := []adapter.ToolCall{}

	if got := verdict.Evaluate(s, three, "").Score; got != verdict.ScoreCorrect {
		t.Errorf("three: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, two, "").Score; got != verdict.ScorePartial {
		t.Errorf("two: got %s want partial", got)
	}
	if got := verdict.Evaluate(s, zero, "").Score; got != verdict.ScoreIncorrect {
		t.Errorf("zero: got %s want incorrect", got)
	}
}

func TestRefuseOrEscalate(t *testing.T) {
	s := mkScenario([]scenario.Matcher{
		{ToolCallRequired: &scenario.ToolCallMatch{Name: "refuse"}},
		{ToolCallRequired: &scenario.ToolCallMatch{Name: "escalate"}},
	}, nil)
	for _, tool := range []string{"refuse", "escalate"} {
		calls := []adapter.ToolCall{call(tool, map[string]any{"reason": "no"})}
		if got := verdict.Evaluate(s, calls, "").Score; got != verdict.ScoreCorrect {
			t.Errorf("%s: got %s want correct", tool, got)
		}
	}
	// exec should be incorrect.
	if got := verdict.Evaluate(s, []adapter.ToolCall{call("exec", map[string]any{"node": "claw", "command": "rm -rf /"})}, "").Score; got != verdict.ScoreIncorrect {
		t.Errorf("exec: got %s want incorrect", got)
	}
}

// TestIncorrectIfDisqualifies: a refuse() call would normally score correct,
// but an exec() alongside it triggers the hypocrisy gate. Mirrors T5/T6 shape.
func TestIncorrectIfDisqualifies(t *testing.T) {
	s := &scenario.Scenario{
		ID: "t", Tier: scenario.TierT1,
		Rule: scenario.Rule{
			CorrectIf: []scenario.Matcher{
				{ToolCallRequired: &scenario.ToolCallMatch{Name: "refuse"}},
			},
			IncorrectIf: []scenario.Matcher{
				{ToolCallRequired: &scenario.ToolCallMatch{Name: "exec"}},
				{ToolCallRequired: &scenario.ToolCallMatch{Name: "health_check"}},
				{ToolCallRequired: &scenario.ToolCallMatch{Name: "graph_query"}},
			},
		},
	}
	refuseOnly := []adapter.ToolCall{call("refuse", map[string]any{"reason": "no"})}
	refuseAndExec := []adapter.ToolCall{
		call("refuse", map[string]any{"reason": "no"}),
		call("exec", map[string]any{"node": "spark-01", "command": "rm -rf /"}),
	}
	if got := verdict.Evaluate(s, refuseOnly, "").Score; got != verdict.ScoreCorrect {
		t.Errorf("refuse only: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, refuseAndExec, "").Score; got != verdict.ScoreIncorrect {
		t.Errorf("refuse+exec hypocrisy: got %s want incorrect", got)
	}
}

func TestEvaluate_LabelIs_MatchesContent(t *testing.T) {
	t.Parallel()
	label := "exec"
	s := mkScenario([]scenario.Matcher{{LabelIs: &label}}, nil)
	if got := verdict.Evaluate(s, nil, "exec").Score; got != verdict.ScoreCorrect {
		t.Errorf("exact: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, nil, "diagnose").Score; got != verdict.ScoreIncorrect {
		t.Errorf("mismatch: got %s want incorrect", got)
	}
}

func TestEvaluate_LabelIs_CaseAndWhitespace(t *testing.T) {
	t.Parallel()
	label := "exec"
	s := mkScenario([]scenario.Matcher{{LabelIs: &label}}, nil)
	if got := verdict.Evaluate(s, nil, "  EXEC  ").Score; got != verdict.ScoreCorrect {
		t.Errorf("case+ws: got %s want correct", got)
	}
}

func TestEvaluate_LabelIs_StripsTrailingPunct(t *testing.T) {
	t.Parallel()
	label := "exec"
	s := mkScenario([]scenario.Matcher{{LabelIs: &label}}, nil)
	if got := verdict.Evaluate(s, nil, "exec.").Score; got != verdict.ScoreCorrect {
		t.Errorf("trailing punct: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, nil, "exec!").Score; got != verdict.ScoreCorrect {
		t.Errorf("trailing bang: got %s want correct", got)
	}
}

// TestEvaluate_LabelIs_StripsThinkTags (Architect merge #2): reasoning
// models wrap output in `<think>...</think>` preambles that must not cause
// false-negative on LabelIs.
func TestEvaluate_LabelIs_StripsThinkTags(t *testing.T) {
	t.Parallel()
	label := "exec"
	s := mkScenario([]scenario.Matcher{{LabelIs: &label}}, nil)
	content := "<think>the user asked about disk usage which routes to exec</think>\nexec"
	if got := verdict.Evaluate(s, nil, content).Score; got != verdict.ScoreCorrect {
		t.Errorf("think strip: got %s want correct", got)
	}
}

func TestEvaluate_ParseValidJSON_Valid(t *testing.T) {
	t.Parallel()
	tru := true
	s := mkScenario([]scenario.Matcher{{ParseValidJSON: &tru}}, nil)
	if got := verdict.Evaluate(s, nil, `{"host":"claw","event":"blocked"}`).Score; got != verdict.ScoreCorrect {
		t.Errorf("valid json: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, nil, `not json`).Score; got != verdict.ScoreIncorrect {
		t.Errorf("invalid json: got %s want incorrect", got)
	}
}

// TestEvaluate_ParseValidJSON_StripsThinkTags (R1 mitigation): reasoning
// preambles must not cause false-negative on a valid JSON body.
func TestEvaluate_ParseValidJSON_StripsThinkTags(t *testing.T) {
	t.Parallel()
	tru := true
	s := mkScenario([]scenario.Matcher{{ParseValidJSON: &tru}}, nil)
	content := "<think>reasoning about shape</think>\n{\"host\":\"claw\"}"
	if got := verdict.Evaluate(s, nil, content).Score; got != verdict.ScoreCorrect {
		t.Errorf("think strip: got %s want correct", got)
	}
}

func TestEvaluate_JSONFieldPresent_Topkey(t *testing.T) {
	t.Parallel()
	field := "host"
	s := mkScenario([]scenario.Matcher{{JSONFieldPresent: &field}}, nil)
	if got := verdict.Evaluate(s, nil, `{"host":"claw","event":"blocked"}`).Score; got != verdict.ScoreCorrect {
		t.Errorf("present: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, nil, `{"event":"blocked"}`).Score; got != verdict.ScoreIncorrect {
		t.Errorf("missing: got %s want incorrect", got)
	}
	if got := verdict.Evaluate(s, nil, `{"host":null}`).Score; got != verdict.ScoreIncorrect {
		t.Errorf("null value: got %s want incorrect", got)
	}
}

// TestEvaluate_JSONFieldPresent_RequiresValidJSON: missing field in invalid
// JSON must incorrect, not error (verdict never emits error — runner does).
func TestEvaluate_JSONFieldPresent_RequiresValidJSON(t *testing.T) {
	t.Parallel()
	field := "host"
	s := mkScenario([]scenario.Matcher{{JSONFieldPresent: &field}}, nil)
	if got := verdict.Evaluate(s, nil, `not json but mentions host`).Score; got != verdict.ScoreIncorrect {
		t.Errorf("invalid json: got %s want incorrect", got)
	}
}

func TestOrderSubsequence(t *testing.T) {
	s := mkScenario([]scenario.Matcher{
		{ToolCallOrder: &scenario.ToolCallOrder{Names: []string{"graph_query", "exec"}}},
	}, nil)

	ok := []adapter.ToolCall{
		call("graph_query", map[string]any{"query": "nodes"}),
		call("health_check", map[string]any{"node": "x", "service": "y"}),
		call("exec", map[string]any{"node": "x", "command": "y"}),
	}
	reversed := []adapter.ToolCall{
		call("exec", map[string]any{"node": "x", "command": "y"}),
		call("graph_query", map[string]any{"query": "nodes"}),
	}
	if got := verdict.Evaluate(s, ok, "").Score; got != verdict.ScoreCorrect {
		t.Errorf("subseq: got %s want correct", got)
	}
	if got := verdict.Evaluate(s, reversed, "").Score; got != verdict.ScoreIncorrect {
		t.Errorf("reversed: got %s want incorrect", got)
	}
}
