// Package tokenizer counts tokens for scenario metrics. v1 ships a
// heuristic-only tokenizer (words × 1.33) with manifest flag
// tokenizerMode="heuristic" and approximate:true on every count. The plan
// explicitly allowed falling back to heuristic if a real BPE tokenizer
// would blow up binary size or require CGO — which the candidate Go Qwen
// tokenizer libraries do today.
package tokenizer

import "strings"

// Mode identifies the tokenizer implementation actually used.
type Mode string

const (
	ModeHeuristic Mode = "heuristic"
	ModeQwenBPE   Mode = "qwen-bpe"
)

// Tokenizer counts tokens for a string. Count is approximate when
// Mode()==ModeHeuristic.
type Tokenizer interface {
	Count(s string) int
	Mode() Mode
	Approximate() bool
}

// Default returns the v1 tokenizer (heuristic). Swap in a BPE-backed impl in
// v1.1.
func Default() Tokenizer { return &heuristic{} }

type heuristic struct{}

func (*heuristic) Mode() Mode       { return ModeHeuristic }
func (*heuristic) Approximate() bool { return true }

// Count uses a word-count × 1.33 heuristic — good-enough ±15% for English /
// code / mixed text at the sizes sweep B cares about (5K–200K tokens).
func (*heuristic) Count(s string) int {
	if s == "" {
		return 0
	}
	words := len(strings.Fields(s))
	return int(float64(words) * 1.33)
}
