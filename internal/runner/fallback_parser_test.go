package runner_test

import (
	"testing"

	"github.com/gresham/resolver/internal/runner"
)

func TestFallbackParser(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantN    int
		wantName string
		wantArgs map[string]any
	}{
		{
			name:     "named double-quoted",
			in:       `exec(node="spark-01", command="docker ps")`,
			wantN:    1,
			wantName: "exec",
			wantArgs: map[string]any{"node": "spark-01", "command": "docker ps"},
		},
		{
			name:     "positional",
			in:       `health_check("spark-01", "vllm-35b")`,
			wantN:    1,
			wantName: "health_check",
			wantArgs: map[string]any{"node": "spark-01", "service": "vllm-35b"},
		},
		{
			name:     "mixed quotes",
			in:       `exec(node='spark-01', command="nvidia-smi --query-gpu=utilization.gpu --format=csv")`,
			wantN:    1,
			wantName: "exec",
			wantArgs: map[string]any{"node": "spark-01", "command": "nvidia-smi --query-gpu=utilization.gpu --format=csv"},
		},
		{
			name:     "nested parens in string",
			in:       `graph_query(query="what depends on llm-proxy (the vLLM proxy service)")`,
			wantN:    1,
			wantName: "graph_query",
			wantArgs: map[string]any{"query": "what depends on llm-proxy (the vLLM proxy service)"},
		},
		{
			name:  "multi call",
			in:    `graph_query(query="list nodes") and then exec(node="claw", command="df -h")`,
			wantN: 2,
		},
		{
			name:     "escalate reason",
			in:       `escalate(reason="this migration is too complex to automate")`,
			wantN:    1,
			wantName: "escalate",
			wantArgs: map[string]any{"reason": "this migration is too complex to automate"},
		},
		{
			name:  "malformed unbalanced parens",
			in:    `exec(node="spark-01"`,
			wantN: 0,
		},
		{
			name:  "completely unrelated text",
			in:    `I would run docker ps on spark-01`,
			wantN: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runner.ParseFallbackToolCalls(tc.in)
			if len(got) != tc.wantN {
				t.Fatalf("got %d calls, want %d: %+v", len(got), tc.wantN, got)
			}
			if tc.wantN == 0 {
				return
			}
			if tc.wantName != "" && got[0].Name != tc.wantName {
				t.Errorf("name: got %q want %q", got[0].Name, tc.wantName)
			}
			for k, want := range tc.wantArgs {
				if gotV, ok := got[0].Arguments[k]; !ok {
					t.Errorf("missing arg %q", k)
				} else if gotV != want {
					t.Errorf("arg %q: got %v want %v", k, gotV, want)
				}
			}
		})
	}
}

func TestFallbackParserNoPanic(t *testing.T) {
	// Fuzz-lite: assorted pathological inputs should never panic.
	inputs := []string{
		``,
		`(((`,
		`"""`,
		`exec)`,
		`exec(`,
		`exec(unterminated="oops`,
		`random text with parens ( and ) and = sign but no tool`,
	}
	for _, in := range inputs {
		_ = runner.ParseFallbackToolCalls(in)
	}
}
