package aggregate_test

import (
	"testing"

	"github.com/wentbackward/resolver/internal/aggregate"
)

func TestNormalizeModel(t *testing.T) {
	// Equivalence: all three forms must produce the same key.
	equiv := []string{
		"Qwen/Qwen3.6-35B-A3B-FP8",
		"Qwen3.6-35B-A3B",
		"qwen3.6-35b-a3b",
	}
	want := "qwen3.6-35b-a3b"
	for _, in := range equiv {
		if got := aggregate.NormalizeModel(in); got != want {
			t.Errorf("NormalizeModel(%q) = %q, want %q", in, got, want)
		}
	}

	// Non-match: Qwen3.5 (not 3.6) must produce a different key.
	different := aggregate.NormalizeModel("Qwen3.5-35B-A3B")
	if different == want {
		t.Errorf("NormalizeModel(Qwen3.5-35B-A3B) = %q, should differ from %q", different, want)
	}

	// FP16 suffix stripping.
	if got := aggregate.NormalizeModel("Meta/Llama-3.1-8B-FP16"); got != "llama-3.1-8b" {
		t.Errorf("fp16 strip: got %q", got)
	}

	// Multiple quantization suffixes stripped cleanly.
	if got := aggregate.NormalizeModel("some/Model-INT4-AWQ"); got != "model" {
		t.Errorf("multi-suffix strip: got %q", got)
	}

	// Separator collapse: double underscore → single dash.
	if got := aggregate.NormalizeModel("qwen3.6-35b__a3b"); got != "qwen3.6-35b-a3b" {
		t.Errorf("sep collapse: got %q", got)
	}

	// Idempotency: NormalizeModel(NormalizeModel(x)) == NormalizeModel(x).
	idempotentCases := []string{
		"Qwen/Qwen3.6-35B-A3B-FP8",
		"qwen3.6-35b-a3b",
		"Meta/Llama-3.1-8B-FP16",
		"some/Model-INT4-AWQ",
		"plain-model-name",
		"Org/Model.Name_v2-GPTQ",
	}
	for _, in := range idempotentCases {
		once := aggregate.NormalizeModel(in)
		twice := aggregate.NormalizeModel(once)
		if once != twice {
			t.Errorf("not idempotent: NormalizeModel(%q) = %q, NormalizeModel again = %q", in, once, twice)
		}
	}
}
