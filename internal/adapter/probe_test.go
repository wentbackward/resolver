package adapter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wentbackward/resolver/internal/adapter"
)

// TestResolveRealModelSuccess exercises the happy path: /v1/models returns
// the standard OpenAI envelope {data:[{id,root}]}. We use `root` when set,
// fall back to `id`.
func TestResolveRealModelSuccess(t *testing.T) {
	cases := map[string]struct {
		body string
		want string
	}{
		"id only": {
			body: `{"data":[{"id":"Qwen/Qwen3.5-35B-A3B-FP8"}]}`,
			want: "Qwen/Qwen3.5-35B-A3B-FP8",
		},
		"root preferred": {
			body: `{"data":[{"id":"gresh-general","root":"Qwen/Qwen3.6-35B-A3B-FP8"}]}`,
			want: "Qwen/Qwen3.6-35B-A3B-FP8",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					t.Errorf("probe hit wrong path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			// Endpoint passed to the adapter includes the chat-completions path —
			// the probe is expected to strip /v1/... and use the origin.
			ad := adapter.NewOpenAIChat(srv.URL + "/v1/chat/completions")
			got := ad.ResolveRealModel(context.Background(), "")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestResolveRealModelNon2xx ensures a 401/403/500 returns "unknown" rather
// than crashing or leaking the error body to the scorecard.
func TestResolveRealModelNon2xx(t *testing.T) {
	for _, code := range []int{401, 403, 404, 500, 502} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":"denied"}`))
		}))
		ad := adapter.NewOpenAIChat(srv.URL + "/v1/chat/completions")
		got := ad.ResolveRealModel(context.Background(), "")
		if got != "unknown" {
			t.Errorf("http %d: got %q, want unknown", code, got)
		}
		srv.Close()
	}
}

// TestResolveRealModelMalformed covers empty body, non-JSON body, JSON with
// no data array. All must degrade to "unknown".
func TestResolveRealModelMalformed(t *testing.T) {
	bodies := []string{
		``,
		`not json`,
		`{"no_data":true}`,
		`{"data":[]}`,
		`{"data":[{"id":""}]}`,
	}
	for _, b := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(b))
		}))
		ad := adapter.NewOpenAIChat(srv.URL + "/v1/chat/completions")
		got := ad.ResolveRealModel(context.Background(), "")
		if got != "unknown" {
			t.Errorf("body %q: got %q, want unknown", b, got)
		}
		srv.Close()
	}
}

// TestResolveRealModelTimeout simulates a slow upstream. The probe must
// abort within the 5s internal deadline regardless of the caller's
// context.
func TestResolveRealModelTimeout(t *testing.T) {
	// Handler blocks briefly past our 500ms client timeout but short enough
	// that httptest.Server teardown isn't penalised.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	ad := adapter.NewOpenAIChat(srv.URL + "/v1/chat/completions")
	// Force the adapter's client to honour the probe's internal ctx deadline.
	ad.HTTPClient = &http.Client{Timeout: 500 * time.Millisecond}

	done := make(chan string, 1)
	go func() { done <- ad.ResolveRealModel(context.Background(), "") }()
	select {
	case got := <-done:
		if got != "unknown" {
			t.Errorf("timeout path: got %q, want unknown", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ResolveRealModel did not honour deadline within 3s (probe should time out internally at 5s max, but HTTPClient.Timeout=500ms should close it sooner)")
	}
}

// TestResolveRealModelUnreachable: closed port / connection refused.
func TestResolveRealModelUnreachable(t *testing.T) {
	ad := adapter.NewOpenAIChat("http://127.0.0.1:1/v1/chat/completions") // port 1 = never bound
	ad.HTTPClient = &http.Client{Timeout: 500 * time.Millisecond}
	got := ad.ResolveRealModel(context.Background(), "")
	if got != "unknown" {
		t.Errorf("unreachable: got %q, want unknown", got)
	}
}
