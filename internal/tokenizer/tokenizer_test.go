package tokenizer_test

import (
	"strings"
	"testing"

	"github.com/gresham/resolver/internal/tokenizer"
)

func TestHeuristicCount(t *testing.T) {
	tok := tokenizer.Default()
	if tok.Mode() != tokenizer.ModeHeuristic {
		t.Errorf("mode = %s, want heuristic", tok.Mode())
	}
	if !tok.Approximate() {
		t.Error("heuristic should report Approximate()=true")
	}
	cases := map[string]int{
		"":                 0,
		"hello":            1, // 1 word * 1.33 → 1 via int()
		"hello world":      2,
		"three words here": 3,
	}
	for in, wantAtLeast := range cases {
		got := tok.Count(in)
		if got < wantAtLeast {
			t.Errorf("Count(%q) = %d, want >= %d", in, got, wantAtLeast)
		}
	}
}

func TestHeuristicScalesWithLength(t *testing.T) {
	tok := tokenizer.Default()
	short := tok.Count("lorem ipsum dolor")
	long := tok.Count(strings.Repeat("lorem ipsum dolor ", 1000))
	if long <= short*10 {
		t.Errorf("long (%d) should be >> short (%d) × 10", long, short)
	}
}
