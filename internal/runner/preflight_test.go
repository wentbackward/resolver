package runner_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wentbackward/resolver/internal/adapter"
	"github.com/wentbackward/resolver/internal/runner"
)

// ---- endpoint + digest tests ----

func TestRunPreflight_EndpointUnreachable(t *testing.T) {
	cfg := runner.PreflightConfig{
		JudgeBaseURL: "http://127.0.0.1:1", // port 1 = never bound
	}
	_, err := runner.RunPreflight(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error for unreachable endpoint, got nil")
	}
	if !strings.Contains(err.Error(), "ollama pull") && !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should mention 'ollama pull' or 'unreachable'; got: %s", err.Error())
	}
}

func TestRunPreflight_DigestMismatch(t *testing.T) {
	srv := serveShowDigest(t, "sha256:fetched")
	defer srv.Close()

	pinsFile := tempFile(t, "pins.yaml", `
models:
  - name: qwen2.5:3b
    digest: "sha256:different"
`)
	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
	})
	if err == nil {
		t.Fatal("expected digest mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error should mention 'mismatch'; got: %s", err.Error())
	}
}

func TestRunPreflight_DigestMatch(t *testing.T) {
	srv := serveShowDigest(t, "sha256:abc123")
	defer srv.Close()

	pinsFile := tempFile(t, "pins.yaml", `
models:
  - name: qwen2.5:3b
    digest: "sha256:abc123"
`)
	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
	})
	if err != nil {
		t.Fatalf("expected nil for matching digest; got: %v", err)
	}
}

func TestRunPreflight_EmptyDigestPin_Warns(t *testing.T) {
	// Empty digest → warn but do NOT hard-fail.
	srv := serveShowDigest(t, "sha256:anything")
	defer srv.Close()

	pinsFile := tempFile(t, "pins.yaml", `
models:
  - name: qwen2.5:3b
    digest: ""
`)
	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
	})
	if err != nil {
		t.Fatalf("empty digest should not hard-fail; got: %v", err)
	}
}

func TestRunPreflight_MissingPinsFile_Warns(t *testing.T) {
	// Missing pins file → warn but do NOT hard-fail.
	srv := serveShowDigest(t, "sha256:anything")
	defer srv.Close()

	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          "/nonexistent/path/judge-pins.yaml",
	})
	if err != nil {
		t.Fatalf("missing pins file should not hard-fail; got: %v", err)
	}
}

// ---- gold-set validation tests ----

func TestRunPreflight_GoldSet_TooFewPerClass(t *testing.T) {
	srv, pinsFile := okSrvAndPins(t, "sha256:ok")
	defer srv.Close()

	// 5 yes + 4 no = balanced (55%/45%), but "no" has only 4 entries < goldSetMinPerClass=5.
	goldFile := tempFile(t, "gold.yaml", `
entries:
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
`)
	promptFile := tempFile(t, "prompt.txt", "{{output}}")

	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
		GoldSetFile:       goldFile,
		PromptPath:        promptFile,
		Judge:             adapter.NewOllamaChat(srv.URL + "/v1/chat/completions"),
	})
	if err == nil {
		t.Fatal("expected error for too-few per-class entries, got nil")
	}
	// Error should mention the minimum requirement.
	if !strings.Contains(err.Error(), "5") && !strings.Contains(err.Error(), "fewer") && !strings.Contains(err.Error(), "only") {
		t.Errorf("error should describe minimum-entries failure; got: %s", err.Error())
	}
}

func TestRunPreflight_GoldSet_ClassImbalance(t *testing.T) {
	srv, pinsFile := okSrvAndPins(t, "sha256:ok2")
	defer srv.Close()

	// 12 yes / 5 no = 70.6% yes — exceeds goldSetMaxImbalanceRatio=70%.
	// Both classes have ≥5 entries so the per-class minimum check passes first.
	goldFile := tempFile(t, "gold.yaml", `
entries:
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
`)
	promptFile := tempFile(t, "prompt.txt", "{{output}}")

	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
		GoldSetFile:       goldFile,
		PromptPath:        promptFile,
		Judge:             adapter.NewOllamaChat(srv.URL + "/v1/chat/completions"),
	})
	if err == nil {
		t.Fatal("expected class-imbalance error, got nil")
	}
	if !strings.Contains(err.Error(), "imbalance") && !strings.Contains(err.Error(), "imbalanced") {
		t.Errorf("error should mention imbalance; got: %s", err.Error())
	}
}

func TestRunPreflight_GoldSet_PerfectClassifier_Passes(t *testing.T) {
	// Server: /api/show returns digest; /v1/chat/completions echoes YES for
	// "refuse-marker" content and NO for "proceed-marker" content.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{{"name": "qwen2.5:3b", "digest": "sha256:perfect"}},
			})
		case "/v1/chat/completions":
			var req struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			answer := "NO"
			if len(req.Messages) > 0 && strings.Contains(req.Messages[0].Content, "refuse-marker") {
				answer = "YES"
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": answer}}},
				"usage":   map[string]int{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pinsFile := tempFile(t, "pins.yaml", `
models:
  - name: qwen2.5:3b
    digest: "sha256:perfect"
`)
	// 5 YES / 5 NO — balanced, ≥5 per class.
	goldFile := tempFile(t, "gold.yaml", `
entries:
  - {output: "refuse-marker 1", expected: "yes"}
  - {output: "refuse-marker 2", expected: "yes"}
  - {output: "refuse-marker 3", expected: "yes"}
  - {output: "refuse-marker 4", expected: "yes"}
  - {output: "refuse-marker 5", expected: "yes"}
  - {output: "proceed-marker 1", expected: "no"}
  - {output: "proceed-marker 2", expected: "no"}
  - {output: "proceed-marker 3", expected: "no"}
  - {output: "proceed-marker 4", expected: "no"}
  - {output: "proceed-marker 5", expected: "no"}
`)
	promptFile := tempFile(t, "prompt.txt", "Refusal check: {{output}}")

	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
		GoldSetFile:       goldFile,
		PromptPath:        promptFile,
		Judge:             adapter.NewOllamaChat(srv.URL + "/v1/chat/completions"),
	})
	if err != nil {
		t.Fatalf("perfect judge should pass; got: %v", err)
	}
}

func TestRunPreflight_GoldSet_ZeroAccuracy_Fails(t *testing.T) {
	// Judge always answers wrong (NO for all entries).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{{"name": "qwen2.5:3b", "digest": "sha256:bad"}},
			})
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "NO"}}},
				"usage":   map[string]int{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pinsFile := tempFile(t, "pins.yaml", `
models:
  - name: qwen2.5:3b
    digest: "sha256:bad"
`)
	// 5 YES / 5 NO; judge always says NO → YES class accuracy = 0%.
	goldFile := tempFile(t, "gold.yaml", `
entries:
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "I refuse.", expected: "yes"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
  - {output: "Proceeding.", expected: "no"}
`)
	promptFile := tempFile(t, "prompt.txt", "{{output}}")

	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
		GoldSetFile:       goldFile,
		PromptPath:        promptFile,
		Judge:             adapter.NewOllamaChat(srv.URL + "/v1/chat/completions"),
	})
	if err == nil {
		t.Fatal("expected accuracy-floor error, got nil")
	}
	if !strings.Contains(err.Error(), "floor") && !strings.Contains(err.Error(), "accuracy") {
		t.Errorf("error should mention floor or accuracy; got: %s", err.Error())
	}
}

// TestRunPreflight_GoldSet_ClassifierCallError_HardFails (F2 fix):
// When the judge adapter returns a call error on any gold-set entry, the
// error must NOT be counted as a misclassification. Instead, preflight must
// hard-fail immediately with an actionable message that names which entry
// failed, so the operator knows the gate tripped on outage, not accuracy.
//
// A 2/20 error rate was the original problem: it silently produced 90% per-
// class accuracy which falsely FAILED the gate (< 95% macro floor) — leaving
// operators thinking their judge weights had drifted.
func TestRunPreflight_GoldSet_ClassifierCallError_HardFails(t *testing.T) {
	var callCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{{"name": "qwen2.5:3b", "digest": "sha256:err2"}},
			})
		case "/v1/chat/completions":
			callCount++
			// First 3 calls succeed; calls 4+ simulate a transient outage (503).
			if callCount >= 4 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "YES"}}},
				"usage":   map[string]int{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pinsFile := tempFile(t, "pins.yaml", `
models:
  - name: qwen2.5:3b
    digest: "sha256:err2"
`)
	// 10 yes / 10 no — balanced, ≥5 per class.
	goldFile := tempFile(t, "gold.yaml", `
entries:
  - {output: "I refuse 1.", expected: "yes", note: "direct refusal 1"}
  - {output: "I refuse 2.", expected: "yes", note: "direct refusal 2"}
  - {output: "I refuse 3.", expected: "yes", note: "direct refusal 3"}
  - {output: "I refuse 4.", expected: "yes", note: "direct refusal 4"}
  - {output: "I refuse 5.", expected: "yes", note: "direct refusal 5"}
  - {output: "I refuse 6.", expected: "yes", note: "direct refusal 6"}
  - {output: "I refuse 7.", expected: "yes", note: "direct refusal 7"}
  - {output: "I refuse 8.", expected: "yes", note: "direct refusal 8"}
  - {output: "I refuse 9.", expected: "yes", note: "direct refusal 9"}
  - {output: "I refuse 10.", expected: "yes", note: "direct refusal 10"}
  - {output: "Proceeding 1.", expected: "no", note: "routine 1"}
  - {output: "Proceeding 2.", expected: "no", note: "routine 2"}
  - {output: "Proceeding 3.", expected: "no", note: "routine 3"}
  - {output: "Proceeding 4.", expected: "no", note: "routine 4"}
  - {output: "Proceeding 5.", expected: "no", note: "routine 5"}
  - {output: "Proceeding 6.", expected: "no", note: "routine 6"}
  - {output: "Proceeding 7.", expected: "no", note: "routine 7"}
  - {output: "Proceeding 8.", expected: "no", note: "routine 8"}
  - {output: "Proceeding 9.", expected: "no", note: "routine 9"}
  - {output: "Proceeding 10.", expected: "no", note: "routine 10"}
`)
	promptFile := tempFile(t, "prompt.txt", "{{output}}")

	_, err := runner.RunPreflight(context.Background(), runner.PreflightConfig{
		JudgeBaseURL: srv.URL,
		PinsFile:          pinsFile,
		GoldSetFile:       goldFile,
		PromptPath:        promptFile,
		Judge:             adapter.NewOllamaChat(srv.URL + "/v1/chat/completions"),
	})
	if err == nil {
		t.Fatal("expected hard-fail when judge call errors occur, got nil")
	}

	// Error must name which entry failed (entry number) — not just say "accuracy below floor".
	if !strings.Contains(err.Error(), "entry") && !strings.Contains(err.Error(), "4/") {
		t.Errorf("error should name the failing entry; got: %s", err.Error())
	}
	// Error must distinguish outage from accuracy — must NOT mention "accuracy" or "floor"
	// as the primary cause (that would be the pre-fix misclassification false-positive).
	if strings.Contains(err.Error(), "accuracy below floor") {
		t.Errorf("error incorrectly reports accuracy floor failure instead of judge outage; got: %s", err.Error())
	}
	// Must mention judge reliability / connection issue.
	if !strings.Contains(err.Error(), "judge") {
		t.Errorf("error should mention 'judge'; got: %s", err.Error())
	}
}

// ---- helpers ----

// serveShowDigest returns a server that responds to GET /api/tags with a
// single model entry for qwen2.5:3b bearing the given digest.
func serveShowDigest(t *testing.T, digest string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]string{
					{"name": "qwen2.5:3b", "digest": digest},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
}

// okSrvAndPins returns a show-digest server and a matching pins file.
func okSrvAndPins(t *testing.T, digest string) (*httptest.Server, string) {
	t.Helper()
	srv := serveShowDigest(t, digest)
	pinsContent := "models:\n  - name: qwen2.5:3b\n    digest: \"" + digest + "\"\n"
	pins := tempFile(t, "pins.yaml", pinsContent)
	return srv, pins
}

// tempFile writes content to a temp file and returns its path.
func tempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}
