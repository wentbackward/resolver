package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ollamaOKResponse returns a minimal valid OpenAI-compat response body.
func ollamaOKResponse(content string) string {
	return `{"choices":[{"message":{"role":"assistant","content":"` + content + `"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`
}

// TestOllamaChatEndToEnd spins up a fake server that returns a canned
// OpenAI-style response and verifies the adapter parses it correctly.
func TestOllamaChatEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type: got %q", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, ollamaOKResponse("YES"))
	}))
	defer server.Close()

	ad := NewOllamaChat(server.URL)
	resp, err := ad.Chat(context.Background(), ChatRequest{
		Model:    "qwen2.5:3b",
		Messages: []Message{{Role: "user", Content: "Does the output refuse?"}},
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "YES" {
		t.Errorf("content: got %q want %q", resp.Content, "YES")
	}
	if resp.Usage.PromptTokens != 10 {
		t.Errorf("usage.prompt_tokens: got %d want 10", resp.Usage.PromptTokens)
	}
	if resp.ElapsedMs < 0 {
		t.Errorf("ElapsedMs: got %d (must be ≥ 0)", resp.ElapsedMs)
	}
}

// TestOllamaChatName verifies the adapter's identifier.
func TestOllamaChatName(t *testing.T) {
	ad := NewOllamaChat("")
	if ad.Name() != "ollama-chat" {
		t.Errorf("Name(): got %q want %q", ad.Name(), "ollama-chat")
	}
}

// TestOllamaChatDefaultEndpoint verifies NewOllamaChat("") fills in the
// canonical localhost:11434 default.
func TestOllamaChatDefaultEndpoint(t *testing.T) {
	ad := NewOllamaChat("")
	if ad.Endpoint != defaultOllamaEndpoint {
		t.Errorf("Endpoint: got %q want %q", ad.Endpoint, defaultOllamaEndpoint)
	}
}

// TestOllamaChatRetryOn503 verifies that the adapter retries on HTTP 503 and
// ultimately succeeds when the server recovers on the second request.
func TestOllamaChatRetryOn503(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			// First call: simulate ollama loading the model.
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":"model loading"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, ollamaOKResponse("NO"))
	}))
	defer server.Close()

	// Use very short backoff to keep the test fast.
	origBase := ollamaRetryBase
	// Can't reassign const; test relies on the retry succeeding within
	// maxRetries (ollamaMaxRetries=3) — the server recovers on attempt 2.
	_ = origBase

	ad := NewOllamaChat(server.URL)
	resp, err := ad.Chat(context.Background(), ChatRequest{
		Model:    "qwen2.5:3b",
		Messages: []Message{{Role: "user", Content: "q"}},
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.Content != "NO" {
		t.Errorf("content: got %q want %q", resp.Content, "NO")
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 HTTP calls (1 503 + 1 success), got %d", calls.Load())
	}
}

// TestOllamaChatExhaustedRetries verifies that after ollamaMaxRetries+1
// consecutive 503 responses the adapter returns an error.
func TestOllamaChatExhaustedRetries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, `{"error":"overloaded"}`)
	}))
	defer server.Close()

	ad := NewOllamaChat(server.URL)
	_, err := ad.Chat(context.Background(), ChatRequest{
		Model:    "qwen2.5:3b",
		Messages: []Message{{Role: "user", Content: "q"}},
		Timeout:  10 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error after exhausted retries, got nil")
	}
}

// TestOllamaChatContextCancelDuringRetry verifies that a cancelled context
// stops the retry loop promptly.
func TestOllamaChatContextCancelDuringRetry(t *testing.T) {
	// Server always returns 503 so the adapter would retry indefinitely
	// without context cancellation.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ad := NewOllamaChat(server.URL)
	_, err := ad.Chat(ctx, ChatRequest{
		Model:    "qwen2.5:3b",
		Messages: []Message{{Role: "user", Content: "q"}},
	})
	if err == nil {
		t.Fatal("expected error from context cancel, got nil")
	}
}

// TestOllamaChatTrimsTrailingNewline verifies the content trimming for
// ollama text responses that include a trailing newline.
func TestOllamaChatTrimsTrailingNewline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Embed a raw newline in the JSON string.
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "YES\n"}},
			},
			"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	ad := NewOllamaChat(server.URL)
	resp, err := ad.Chat(context.Background(), ChatRequest{
		Model:    "qwen2.5:3b",
		Messages: []Message{{Role: "user", Content: "q"}},
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "YES" {
		t.Errorf("content: got %q want %q (trim failed)", resp.Content, "YES")
	}
}

// TestOllamaChatPerRequestTimeout verifies that a per-request Timeout on
// ChatRequest overrides the adapter's default client timeout.
func TestOllamaChatPerRequestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Simulate a slow response — sleep longer than the per-request timeout.
		time.Sleep(500 * time.Millisecond)
		io.WriteString(w, ollamaOKResponse("YES"))
	}))
	defer server.Close()

	ad := NewOllamaChat(server.URL)
	start := time.Now()
	_, err := ad.Chat(context.Background(), ChatRequest{
		Model:    "qwen2.5:3b",
		Messages: []Message{{Role: "user", Content: "q"}},
		Timeout:  50 * time.Millisecond,
	})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	// The call should have terminated well before the server sleep.
	if elapsed > 2*time.Second {
		t.Errorf("timeout did not fire: elapsed %v", elapsed)
	}
}
