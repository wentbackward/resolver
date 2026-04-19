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
