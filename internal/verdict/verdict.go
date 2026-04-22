// Package verdict scores a tool-call trace against a scenario's validation
// rule. Scores are one of: correct, partial, incorrect, error. Error is set
// by the runner when the HTTP call fails or the body is unparseable; verdict
// never emits error itself.
package verdict

import (
	"context"
	"encoding/json"
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
	// Classifier is populated when a ClassifierMatch matcher fired.
	// nil when no classifier matcher was involved in producing this verdict.
	Classifier *ClassifierMeta
}

// EvaluateOpts carries optional dependencies for Evaluate. All fields are
// optional; the zero value produces the same behaviour as the pre-B3 call
// (no classifier, backward compatible).
type EvaluateOpts struct {
	// Classifier, if non-nil, is used for ClassifierMatch matchers.
	// When nil (including the --no-classifier path), ClassifierMatch matchers
	// are silently skipped (not counted as matches).
	Classifier adapter.Adapter

	// Ctx is the parent context for classifier calls. Defaults to
	// context.Background() when nil.
	Ctx context.Context

	// DataDir is the base directory for resolving prompt_ref paths relative
	// to the data directory (e.g. cmd/resolver/data/). Defaults to "." when
	// empty.
	DataDir string
}

// Evaluate applies a scenario's rule to an observed tool-call trace +
// assistant content. Correct wins over partial wins over incorrect.
//
// When a classifier is injected via EvaluateOpts, ClassifierMatch matchers run
// inline during the primary verdict pass. The ClassifierMeta from the first
// ClassifierMatch encountered is threaded through to Result.Classifier without
// a second call — avoiding the double-fire bug where a sidecar re-invoked the
// same classifier matcher that already ran during the primary pass (F1 fix).
//
// The variadic form preserves backward compatibility — existing callers with
// no opts continue to compile and behave identically.
func Evaluate(s *scenario.Scenario, calls []adapter.ToolCall, content string, opts ...EvaluateOpts) Result {
	var cc *classifierCtx
	if len(opts) > 0 && opts[0].Classifier != nil {
		ctx := opts[0].Ctx
		if ctx == nil {
			ctx = context.Background()
		}
		cc = &classifierCtx{
			cl:      opts[0].Classifier,
			ctx:     ctx,
			dataDir: opts[0].DataDir,
		}
	}

	// Primary verdict: all matcher kinds run normally (ClassifierMatch uses cc
	// when available; is silently skipped when cc is nil / --no-classifier).
	// matchAny now threads out *ClassifierMeta so we can attach it to the
	// result without a redundant second classifier call (F1 fix).
	ok, clMeta, errRes := matchAny(s.Rule.CorrectIf, calls, content, cc)
	if errRes != nil {
		return *errRes
	}
	var primary Result
	if ok {
		primary = Result{Score: ScoreCorrect, Reason: firstNonEmpty(s.Rule.ReasonCorrect, "matched correct_if rule")}
	} else {
		var clMeta2 *ClassifierMeta
		ok, clMeta2, errRes = matchAny(s.Rule.PartialIf, calls, content, cc)
		if errRes != nil {
			return *errRes
		}
		if clMeta == nil {
			clMeta = clMeta2 // use partial_if meta when correct_if had none
		}
		if ok {
			primary = Result{Score: ScorePartial, Reason: firstNonEmpty(s.Rule.ReasonPartial, "matched partial_if rule")}
		} else {
			primary = Result{Score: ScoreIncorrect, Reason: firstNonEmpty(s.Rule.ReasonIncorrect, "did not match correct_if or partial_if")}
		}
	}

	// Attach classifier meta. When the primary pass already evaluated a
	// ClassifierMatch (clMeta != nil), reuse that result — no second call.
	// Only fall back to the sidecar when no ClassifierMatch appeared in the
	// primary matchers at all (e.g. classifier_match lives only in partial_if
	// of a scenario that matched correct_if via a structural rule).
	if cc != nil {
		if clMeta != nil {
			primary.Classifier = clMeta
		} else {
			primary.Classifier = runClassifierSidecar(s, content, cc)
		}
	}
	return primary
}

// runClassifierSidecar finds the first ClassifierMatch matcher across
// correct_if and partial_if, runs it, and returns the ClassifierMeta.
// Called only when the primary matchAny pass did not encounter any
// ClassifierMatch (clMeta == nil), preventing double-fire (F1).
// Returns nil when no ClassifierMatch matcher is present.
func runClassifierSidecar(s *scenario.Scenario, content string, cc *classifierCtx) *ClassifierMeta {
	all := append(s.Rule.CorrectIf, s.Rule.PartialIf...)
	for _, m := range all {
		if m.ClassifierMatch == nil {
			continue
		}
		cr := callClassifier(cc, cc.promptPath(m.ClassifierMatch.PromptRef), content)
		r := interpretClassifier(cr, m.ClassifierMatch)
		return r.Classifier
	}
	return nil
}

// matchAny returns (true, meta, nil) if at least one Matcher evaluates to
// true, (false, meta, &Result{ScoreError, ...}) if a ClassifierMatch returns
// an error, or (false, nil, nil) if no matcher matches. The *ClassifierMeta
// is non-nil whenever a ClassifierMatch fired (regardless of YES/NO/error)
// so callers can reuse it without a second classifier call (F1 fix).
func matchAny(ms []scenario.Matcher, calls []adapter.ToolCall, content string, cc *classifierCtx) (bool, *ClassifierMeta, *Result) {
	for _, m := range ms {
		ok, meta, errRes := matchOne(m, calls, content, cc)
		if errRes != nil {
			return false, meta, errRes
		}
		if ok {
			return true, meta, nil
		}
	}
	return false, nil, nil
}

// matchOne evaluates a single Matcher. Returns (matched, meta, nil) for all
// matchers; meta is non-nil only when a ClassifierMatch fired. Returns
// (false, meta, &Result{ScoreError}) when a ClassifierMatch call fails or
// returns a non-YES/NO answer; (false, nil, nil) when ClassifierMatch is
// skipped because no classifier is injected.
func matchOne(m scenario.Matcher, calls []adapter.ToolCall, content string, cc *classifierCtx) (bool, *ClassifierMeta, *Result) {
	switch {
	case m.ToolCallRequired != nil:
		return hasToolCallMatch(calls, *m.ToolCallRequired), nil, nil
	case m.ToolCallForbidden != nil:
		return !hasToolCallMatch(calls, *m.ToolCallForbidden), nil, nil
	case m.ToolCallOrder != nil:
		return isSubsequence(calls, m.ToolCallOrder.Names), nil, nil
	case m.ToolCallCountAtLeast != nil:
		return countMatches(calls, m.ToolCallCountAtLeast.Name, m.ToolCallCountAtLeast.ArgsRegex) >= m.ToolCallCountAtLeast.Min, nil, nil
	case m.ToolCallCountInRange != nil:
		c := countMatches(calls, m.ToolCallCountInRange.Name, m.ToolCallCountInRange.ArgsRegex)
		return c >= m.ToolCallCountInRange.Min && c <= m.ToolCallCountInRange.Max, nil, nil
	case m.RegexMatch != nil:
		return regexMatchesTarget(*m.RegexMatch, calls, content), nil, nil
	case m.AnyToolCall != nil:
		return hasToolCallMatch(calls, *m.AnyToolCall), nil, nil
	case m.LabelIs != nil:
		return labelMatches(content, *m.LabelIs), nil, nil
	case m.ParseValidJSON != nil:
		if !*m.ParseValidJSON {
			return false, nil, nil
		}
		return json.Valid([]byte(stripThinkAndTrim(content))), nil, nil
	case m.JSONFieldPresent != nil:
		return jsonFieldPresent(content, *m.JSONFieldPresent), nil, nil
	case m.ClassifierMatch != nil:
		if cc == nil {
			// No classifier injected (--no-classifier path); skip silently.
			return false, nil, nil
		}
		cr := callClassifier(cc, cc.promptPath(m.ClassifierMatch.PromptRef), content)
		r := interpretClassifier(cr, m.ClassifierMatch)
		// Always return the meta so callers can reuse it (F1 fix: prevents
		// double-fire when classifier_match is first in correct_if).
		switch r.Score {
		case ScoreCorrect:
			return true, r.Classifier, nil
		case ScoreIncorrect:
			return false, r.Classifier, nil
		default: // ScoreError
			return false, r.Classifier, &r
		}
	}
	return false, nil, nil
}

// thinkTagRe matches reasoning-model `<think>...</think>` preambles. Must be
// multi-line (the body can span several lines) and non-greedy (models can
// emit more than one preamble).
var thinkTagRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// stripThinkAndTrim removes `<think>...</think>` preambles from reasoning
// models and trims surrounding whitespace. Shared by LabelIs, ParseValidJSON,
// and JSONFieldPresent so reasoning-model captures don't false-negative.
func stripThinkAndTrim(content string) string {
	return strings.TrimSpace(thinkTagRe.ReplaceAllString(content, ""))
}

// labelPunctRe matches trailing punctuation a classifier model may append to
// its single-label output (e.g. `"exec."`, `"diagnose!"`).
var labelPunctRe = regexp.MustCompile(`[\s\p{P}]+$`)

// labelMatches returns true when, after stripping <think> preambles,
// surrounding whitespace, trailing punctuation, and lowercasing, the
// assistant content equals the configured label (also lowercased).
func labelMatches(content, label string) bool {
	norm := strings.ToLower(stripThinkAndTrim(content))
	norm = labelPunctRe.ReplaceAllString(norm, "")
	want := strings.ToLower(strings.TrimSpace(label))
	return norm == want
}

// jsonFieldPresent returns true when the assistant content parses as a JSON
// object and the named top-level field is present with a non-null value.
func jsonFieldPresent(content, field string) bool {
	body := stripThinkAndTrim(content)
	if !json.Valid([]byte(body)) {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return false
	}
	raw, ok := obj[field]
	if !ok {
		return false
	}
	return string(raw) != "null"
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
