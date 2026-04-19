// Package verdict scores a tool-call trace against a scenario's validation
// rule. Scores are one of: correct, partial, incorrect, error. Error is set
// by the runner when the HTTP call fails or the body is unparseable; verdict
// never emits error itself.
package verdict

import (
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/scenario"
)

// Score is one of the four spec values.
type Score string

const (
	ScoreCorrect   Score = "correct"
	ScorePartial   Score = "partial"
	ScoreIncorrect Score = "incorrect"
	ScoreError     Score = "error"
)

// Result is the per-query verdict output.
type Result struct {
	Score  Score
	Reason string
}

// Evaluate applies a scenario's rule to an observed tool-call trace +
// assistant content. Correct wins over partial wins over incorrect.
func Evaluate(s *scenario.Scenario, calls []adapter.ToolCall, content string) Result {
	if matchAny(s.Rule.CorrectIf, calls, content) {
		return Result{Score: ScoreCorrect, Reason: firstNonEmpty(s.Rule.ReasonCorrect, "matched correct_if rule")}
	}
	if matchAny(s.Rule.PartialIf, calls, content) {
		return Result{Score: ScorePartial, Reason: firstNonEmpty(s.Rule.ReasonPartial, "matched partial_if rule")}
	}
	return Result{Score: ScoreIncorrect, Reason: firstNonEmpty(s.Rule.ReasonIncorrect, "did not match correct_if or partial_if")}
}

// matchAny returns true if at least one Matcher in the list evaluates to
// true.
func matchAny(ms []scenario.Matcher, calls []adapter.ToolCall, content string) bool {
	for _, m := range ms {
		if matchOne(m, calls, content) {
			return true
		}
	}
	return false
}

func matchOne(m scenario.Matcher, calls []adapter.ToolCall, content string) bool {
	switch {
	case m.ToolCallRequired != nil:
		return hasToolCallMatch(calls, *m.ToolCallRequired)
	case m.ToolCallForbidden != nil:
		return !hasToolCallMatch(calls, *m.ToolCallForbidden)
	case m.ToolCallOrder != nil:
		return isSubsequence(calls, m.ToolCallOrder.Names)
	case m.ToolCallCountAtLeast != nil:
		return countMatches(calls, m.ToolCallCountAtLeast.Name, m.ToolCallCountAtLeast.ArgsRegex) >= m.ToolCallCountAtLeast.Min
	case m.ToolCallCountInRange != nil:
		c := countMatches(calls, m.ToolCallCountInRange.Name, m.ToolCallCountInRange.ArgsRegex)
		return c >= m.ToolCallCountInRange.Min && c <= m.ToolCallCountInRange.Max
	case m.RegexMatch != nil:
		return regexMatchesTarget(*m.RegexMatch, calls, content)
	case m.AnyToolCall != nil:
		return hasToolCallMatch(calls, *m.AnyToolCall)
	}
	return false
}

// hasToolCallMatch: at least one call matches the name (if given) and every
// args_regex entry (case-insensitive).
func hasToolCallMatch(calls []adapter.ToolCall, tm scenario.ToolCallMatch) bool {
	for _, c := range calls {
		if tm.Name != "" && c.Name != tm.Name {
			continue
		}
		if argsRegexMatch(c, tm.ArgsRegex) {
			return true
		}
	}
	return false
}

func countMatches(calls []adapter.ToolCall, name string, argsRe map[string]string) int {
	n := 0
	for _, c := range calls {
		if name != "" && c.Name != name {
			continue
		}
		if argsRegexMatch(c, argsRe) {
			n++
		}
	}
	return n
}

func argsRegexMatch(c adapter.ToolCall, argsRe map[string]string) bool {
	for k, re := range argsRe {
		val, ok := c.Arguments[k]
		if !ok {
			return false
		}
		s, _ := val.(string)
		if s == "" {
			// Accept non-string values coerced to JSON-ish repr.
			s = stringifyAny(val)
		}
		if !iMatch(re, s) {
			return false
		}
	}
	return true
}

func regexMatchesTarget(rm scenario.RegexMatch, calls []adapter.ToolCall, content string) bool {
	switch rm.Target {
	case "content", "":
		return iMatch(rm.Pattern, content)
	case "any_tool_call_args":
		for _, c := range calls {
			joined := joinArgs(c)
			if iMatch(rm.Pattern, joined) {
				return true
			}
		}
		return false
	case "tool_call_args":
		for _, c := range calls {
			if rm.Name != "" && c.Name != rm.Name {
				continue
			}
			if rm.Field == "" {
				if iMatch(rm.Pattern, joinArgs(c)) {
					return true
				}
				continue
			}
			val, ok := c.Arguments[rm.Field]
			if !ok {
				continue
			}
			s, _ := val.(string)
			if s == "" {
				s = stringifyAny(val)
			}
			if iMatch(rm.Pattern, s) {
				return true
			}
		}
		return false
	}
	return false
}

// isSubsequence returns true if names appear in calls in that order (not
// necessarily contiguous).
func isSubsequence(calls []adapter.ToolCall, names []string) bool {
	i := 0
	for _, c := range calls {
		if i >= len(names) {
			return true
		}
		if c.Name == names[i] {
			i++
		}
	}
	return i >= len(names)
}

func joinArgs(c adapter.ToolCall) string {
	parts := make([]string, 0, len(c.Arguments))
	for k, v := range c.Arguments {
		parts = append(parts, k+"="+stringifyAny(v))
	}
	return strings.Join(parts, " ")
}

func stringifyAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return toString(v)
}

// toString coerces a JSON-decoded scalar to its string form for regex
// matching. Uses strconv to preserve fractional floats (the earlier
// handrolled version silently truncated).
func toString(v any) string {
	switch x := v.(type) {
	case int:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return ""
}

// iMatch: case-insensitive regex match. Compile results cached.
var reCache = sync.Map{}

func iMatch(pattern, s string) bool {
	if pattern == "" {
		return false
	}
	c, ok := reCache.Load(pattern)
	if !ok {
		compiled, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			return false
		}
		reCache.Store(pattern, compiled)
		c = compiled
	}
	return c.(*regexp.Regexp).MatchString(s)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
