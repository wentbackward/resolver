package aggregate

import (
	"regexp"
	"strings"
)

var quantSuffixes = []string{
	"-fp8", "-fp16", "-bf16", "-int8", "-int4", "-gptq", "-awq",
}

// sepRun matches runs of 2+ separators or a standalone underscore; single
// dashes/dots are preserved so version numbers like "3.6" survive.
var sepRun = regexp.MustCompile(`[-_.]{2,}|_`)

// NormalizeModel produces a canonical key so variants of a model name
// (vLLM org prefix, quantization suffixes, casing, separator runs)
// compare equal. Example: "Qwen/Qwen3.6-35B-A3B-FP8" → "qwen3.6-35b-a3b".
// Mirrored by _normalize_model in tools/analyze/src/analyze/db.py.
func NormalizeModel(name string) string {
	s := strings.ToLower(name)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	for {
		stripped := false
		for _, sfx := range quantSuffixes {
			if strings.HasSuffix(s, sfx) {
				s = s[:len(s)-len(sfx)]
				stripped = true
			}
		}
		if !stripped {
			break
		}
	}
	s = sepRun.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}
