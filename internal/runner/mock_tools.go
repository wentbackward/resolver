package runner

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gresham/resolver/internal/adapter"
	"github.com/gresham/resolver/internal/scenario"
	"github.com/gresham/resolver/internal/tokenizer"
)

func regexpMustCompile(p string) *regexp.Regexp { return regexp.MustCompile(p) }

// MockFixturesFS lets the runner read fixture content regardless of whether
// the harness is running with embedded data or a --data-dir override.
type MockFixturesFS interface {
	ReadFixture(path string) ([]byte, error)
	Exists(path string) bool
}

// DirFixtures serves fixtures from an on-disk directory.
type DirFixtures struct {
	Root string
}

func (d DirFixtures) ReadFixture(p string) ([]byte, error) {
	return os.ReadFile(filepath.Join(d.Root, p))
}
func (d DirFixtures) Exists(p string) bool {
	_, err := os.Stat(filepath.Join(d.Root, p))
	return err == nil
}

// FSFixtures serves fixtures from a generic fs.FS (e.g. embed.FS).
type FSFixtures struct {
	FS fs.FS
}

func (f FSFixtures) ReadFixture(p string) ([]byte, error) {
	return fs.ReadFile(f.FS, p)
}
func (f FSFixtures) Exists(p string) bool {
	_, err := fs.Stat(f.FS, p)
	return err == nil
}

// BuildMockTools returns the v1 tool registry: read_document, web_search,
// fetch_api. Each consumes fixture ids declared on the scenario.
func BuildMockTools(fx MockFixturesFS, tok tokenizer.Tokenizer) map[string]MockToolFunc {
	if tok == nil {
		tok = tokenizer.Default()
	}
	return map[string]MockToolFunc{
		"read_document": readDocument(fx),
		"web_search":    webSearch(fx),
		"fetch_api":     fetchAPI(fx),
		// graph_query is used in Tier 1 against a live endpoint, but when
		// referenced in a multi-turn scenario the caller can provide a
		// per-scenario mock via additional registry overlays.
	}
}

// readDocument: takes a { "id": "fixture-id" } or { "path": "docs/foo.md" }
// argument and returns the fixture body as a JSON-encoded document. If the
// requested id/path doesn't resolve and the scenario declares fixtures,
// falls through to the first declared fixture so the scenario still makes
// progress (the model usually can't guess fixture ids).
func readDocument(fx MockFixturesFS) MockToolFunc {
	return func(call adapter.ToolCall, s *scenario.Scenario) string {
		path, ok := resolveFixturePath(call, s, "docs")
		if !ok {
			return jsonErr("missing fixture id/path; scenario.fixtures did not declare one")
		}
		data, err := fx.ReadFixture(path)
		if err != nil {
			// Fallback: first declared fixture.
			if len(s.Fixtures) > 0 {
				fb := filepath.Join("docs", s.Fixtures[0])
				if data2, err2 := fx.ReadFixture(fb); err2 == nil {
					data, err = data2, nil
					path = fb
				}
			}
		}
		if err != nil {
			return jsonErr(fmt.Sprintf("fixture %q: %v", path, err))
		}
		// Strip YAML front-matter if present; models shouldn't see it.
		content := stripFrontmatter(string(data))
		payload := map[string]any{
			"fixture": path,
			"content": content,
		}
		b, _ := json.Marshal(payload)
		return string(b)
	}
}

// webSearch: returns N snippets drawn from the scenario's fixture list.
// Argument: { "query": "...", "limit": 3 } (limit optional).
func webSearch(fx MockFixturesFS) MockToolFunc {
	return func(call adapter.ToolCall, s *scenario.Scenario) string {
		limit := 3
		if v, ok := call.Arguments["limit"]; ok {
			switch x := v.(type) {
			case float64:
				limit = int(x)
			case int:
				limit = x
			}
		}
		if limit > len(s.Fixtures) {
			limit = len(s.Fixtures)
		}
		results := make([]map[string]string, 0, limit)
		for i := 0; i < limit; i++ {
			fid := s.Fixtures[i]
			data, err := fx.ReadFixture(filepath.Join("docs", fid))
			if err != nil {
				continue
			}
			body := stripFrontmatter(string(data))
			results = append(results, map[string]string{
				"title":   fid,
				"snippet": snippet(body, 280),
			})
		}
		b, _ := json.Marshal(map[string]any{"results": results})
		return string(b)
	}
}

// fetchAPI: generic "fetch a URL and return the body". For mocks the URL
// resolves to a fixture file under fixtures/api/{slug}.json. The slug is
// sanitized to [A-Za-z0-9._-]+ so a model-supplied URL can't escape the
// fixtures directory regardless of how filepath.Join handles it.
var fetchSlugSafe = regexpMustCompile(`[^A-Za-z0-9._-]+`)

func fetchAPI(fx MockFixturesFS) MockToolFunc {
	return func(call adapter.ToolCall, s *scenario.Scenario) string {
		url, _ := call.Arguments["url"].(string)
		if url == "" {
			return jsonErr("missing url")
		}
		slug := fetchSlugSafe.ReplaceAllString(url, "_")
		p := filepath.Join("api", slug+".json")
		if !fx.Exists(p) {
			// Fallback: any fixture the scenario declares.
			if len(s.Fixtures) > 0 {
				alt := filepath.Join("api", s.Fixtures[0])
				if fx.Exists(alt) {
					p = alt
				}
			}
		}
		data, err := fx.ReadFixture(p)
		if err != nil {
			return jsonErr(fmt.Sprintf("no mock for %s", url))
		}
		return string(data)
	}
}

func resolveFixturePath(call adapter.ToolCall, s *scenario.Scenario, subdir string) (string, bool) {
	if p, ok := call.Arguments["path"].(string); ok && p != "" {
		return p, true
	}
	if id, ok := call.Arguments["id"].(string); ok && id != "" {
		return filepath.Join(subdir, id), true
	}
	// Fall back to the scenario's first declared fixture.
	if len(s.Fixtures) > 0 {
		return filepath.Join(subdir, s.Fixtures[0]), true
	}
	return "", false
}

func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	end := strings.Index(s[3:], "---")
	if end < 0 {
		return s
	}
	rest := s[3+end+3:]
	return strings.TrimLeft(rest, "\r\n")
}

func snippet(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func jsonErr(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}
