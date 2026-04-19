// Package runner holds the scenario executor, tool-call fallback parser,
// metric aggregation, and multi-turn orchestration.
package runner

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/wentbackward/resolver/internal/adapter"
)

var knownToolNames = map[string]struct{}{
	"exec":         {},
	"health_check": {},
	"graph_query":  {},
	"escalate":     {},
	"refuse":       {},
}

// ParseFallbackToolCalls extracts tool calls from message content when the
// model emits them as text like `exec(node="spark-01", command="docker ps")`
// instead of structured `tool_calls`. Per spec §9. Returns an empty slice
// (never nil panics) on malformed input.
//
// Handles:
//   - Named args: name(key="value", key2="value2")
//   - Positional args: name("a", "b") — mapped in declared order.
//   - Mixed quotes: single, double, backtick.
//   - Nested parens inside string values.
//   - Multiple calls in the same content.
func ParseFallbackToolCalls(content string) []adapter.ToolCall {
	if content == "" {
		return nil
	}
	defer func() {
		// Safety net: the parser is defensive but we never want a panic to
		// leak to the caller. A panic here would score the scenario as an
		// error anyway; better to return [] and let the rule-based verdict
		// treat it as "no calls".
		_ = recover()
	}()

	var out []adapter.ToolCall
	// Walk the content finding known-tool identifiers followed by '(' that
	// are at word boundaries.
	re := regexp.MustCompile(`\b(exec|health_check|graph_query|escalate|refuse)\s*\(`)
	idxs := re.FindAllStringSubmatchIndex(content, -1)
	for _, m := range idxs {
		name := content[m[2]:m[3]]
		// m[1] is the offset of the '(' + 1? No, m[1] is end of whole match.
		// Opening paren is at m[1]-1.
		openParen := m[1] - 1
		if openParen < 0 || openParen >= len(content) || content[openParen] != '(' {
			continue
		}
		close := findMatchingParen(content, openParen)
		if close < 0 {
			continue
		}
		args := parseArgs(name, content[openParen+1:close])
		out = append(out, adapter.ToolCall{
			Name:         name,
			Arguments:    args,
			RawArguments: content[openParen+1 : close],
		})
	}
	return out
}

// findMatchingParen returns the index of the ')' matching the '(' at
// openIdx, or -1 if unbalanced. Handles quoted strings so '(' / ')' inside
// strings don't count.
func findMatchingParen(s string, openIdx int) int {
	depth := 0
	i := openIdx
	for i < len(s) {
		c := s[i]
		switch c {
		case '"', '\'', '`':
			// Skip to matching quote (respecting backslash escapes for " and ').
			quote := c
			i++
			for i < len(s) {
				if s[i] == '\\' && quote != '`' && i+1 < len(s) {
					i += 2
					continue
				}
				if s[i] == quote {
					break
				}
				i++
			}
			if i >= len(s) {
				return -1
			}
			i++
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
		i++
	}
	return -1
}

// parseArgs parses the content between the parens of a tool call invocation.
// Supports:
//   - Named args:   key="value", key2=123, key3=true
//   - Positional:   "spark-01", "docker ps"   -> mapped to schema-declared arg
//                                                names for the given tool.
// If it can't make sense of the args, returns an empty map (scenarios will
// typically score that as incorrect, which is the right signal).
func parseArgs(toolName, inside string) map[string]any {
	inside = strings.TrimSpace(inside)
	if inside == "" {
		return map[string]any{}
	}
	parts := splitTopLevelCommas(inside)
	out := map[string]any{}
	var positional []any
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if k, v, ok := parseNamedArg(p); ok {
			out[k] = v
			continue
		}
		// Positional — parse as literal.
		positional = append(positional, parseLiteral(p))
	}
	if len(positional) > 0 {
		// Map positional args onto the known tool's schema.
		keys := positionalKeys(toolName)
		for i, val := range positional {
			if i >= len(keys) {
				break
			}
			if _, exists := out[keys[i]]; exists {
				continue
			}
			out[keys[i]] = val
		}
	}
	return out
}

// positionalKeys returns the argument key order for a known tool name.
// Matches spec §4 argument schema.
func positionalKeys(tool string) []string {
	switch tool {
	case "exec":
		return []string{"node", "command"}
	case "health_check":
		return []string{"node", "service"}
	case "graph_query":
		return []string{"query"}
	case "escalate":
		return []string{"reason"}
	case "refuse":
		return []string{"reason"}
	}
	return nil
}

// splitTopLevelCommas splits on commas that are not inside strings or nested
// parens/brackets/braces.
func splitTopLevelCommas(s string) []string {
	var parts []string
	var buf strings.Builder
	depth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '"', '\'', '`':
			buf.WriteByte(c)
			quote := c
			i++
			for i < len(s) {
				buf.WriteByte(s[i])
				if s[i] == '\\' && quote != '`' && i+1 < len(s) {
					i++
					buf.WriteByte(s[i])
					i++
					continue
				}
				if s[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, buf.String())
				buf.Reset()
				i++
				continue
			}
		}
		buf.WriteByte(c)
		i++
	}
	if buf.Len() > 0 {
		parts = append(parts, buf.String())
	}
	return parts
}

// parseNamedArg tries `key = "value"` or `key=value`.
func parseNamedArg(s string) (string, any, bool) {
	eq := indexEqOutsideStrings(s)
	if eq < 0 {
		return "", nil, false
	}
	key := strings.TrimSpace(s[:eq])
	val := strings.TrimSpace(s[eq+1:])
	if !isIdentifier(key) {
		return "", nil, false
	}
	return key, parseLiteral(val), true
}

func indexEqOutsideStrings(s string) int {
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '"', '\'', '`':
			quote := c
			i++
			for i < len(s) {
				if s[i] == '\\' && quote != '`' && i+1 < len(s) {
					i += 2
					continue
				}
				if s[i] == quote {
					break
				}
				i++
			}
			if i >= len(s) {
				return -1
			}
			i++
			continue
		case '=':
			return i
		}
		i++
	}
	return -1
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// parseLiteral: string literal with any of the three quote styles, number,
// bool, null, or fallback to string.
func parseLiteral(s string) any {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return ""
	}
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') || (first == '`' && last == '`') {
			return unquoteBasic(s[1 : len(s)-1])
		}
	}
	// Try JSON-ish number / bool / null.
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		return v
	}
	return s
}

func unquoteBasic(s string) string {
	// Minimal de-escaping for \", \', \\, \n, \t.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
				continue
			case 't':
				b.WriteByte('\t')
				i++
				continue
			case 'r':
				b.WriteByte('\r')
				i++
				continue
			case '"', '\'', '\\':
				b.WriteByte(s[i+1])
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
