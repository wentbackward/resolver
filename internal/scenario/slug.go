package scenario

import (
	"regexp"
	"strings"
	"time"
)

// ModelSlug transforms a model name into the scorecard filename slug per spec
// §7: replace each non-[A-Za-z0-9._-] character with _, then collapse runs of _.
func ModelSlug(model string) string {
	reReplace := regexp.MustCompile(`[^A-Za-z0-9._-]`)
	reCollapse := regexp.MustCompile(`_+`)
	out := reReplace.ReplaceAllString(model, "_")
	out = reCollapse.ReplaceAllString(out, "_")
	return strings.Trim(out, "_")
}

// FilenameTimestamp renders a time as ISO-8601 UTC with ':' and '.' replaced
// by '-', truncated to seconds, per spec §7 (e.g. "2026-04-02T14-34-56").
func FilenameTimestamp(t time.Time) string {
	s := t.UTC().Format("2006-01-02T15:04:05")
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

// ScorecardTimestamp is the meta.timestamp string — ISO-8601 UTC with
// millisecond precision and trailing Z, matching spec §7 example.
func ScorecardTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
