package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestDecodeArgumentsStringOrObject exercises the spec §2 tolerance:
// `arguments` may be a JSON string or an object.
func TestDecodeArgumentsStringOrObject(t *testing.T) {
	cases := map[string]struct {
		raw   string
		want  map[string]any
		wantRaw string
	}{
		"object": {
			raw:  `{"node":"spark-01","command":"docker ps"}`,
			want: map[string]any{"node": "spark-01", "command": "docker ps"},
		},
		"string-wrapped object": {
			raw:  `"{\"node\":\"spark-01\",\"command\":\"docker ps\"}"`,
			want: map[string]any{"node": "spark-01", "command": "docker ps"},
		},
		"plain string (unparseable)": {
			raw:    `"not-json-at-all"`,
			want:   map[string]any{},
			wantRaw: "not-json-at-all",
		},
		"null": {
			raw:  `null`,
			want: map[string]any{},
		},
		"empty string": {
			raw:  `""`,
			want: map[string]any{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, raw, err := decodeArguments(json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("arg[%s]: got %v want %v", k, got[k], v)
				}
			}
			if tc.wantRaw != "" && raw != tc.wantRaw {
				t.Errorf("raw: got %q want %q", raw, tc.wantRaw)
			}
		})
	}
}

// TestOpenAIChatEndToEnd spins up a fake server that returns a canned
// OpenAI-style response, verifies the adapter parses it correctly.
func TestOpenAIChatEndToEnd(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Spot-check that temperature + tool_choice made it through.
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if req["tool_choice"] != "auto" {
			t.Errorf("tool_choice: got %v", req["tool_choice"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"role":"assistant","content":null,
			  "tool_calls":[{"id":"x","type":"function","function":{"name":"exec","arguments":"{\"node\":\"spark-01\",\"command\":\"docker ps\"}"}}]
			}}],
			"usage": {"prompt_tokens": 40, "completion_tokens": 12, "total_tokens": 52}
		}`))
	}))
	defer server.Close()

	ad := NewOpenAIChat(server.URL)
	resp, err := ad.Chat(context.Background(), ChatRequest{
		Model:       "test",
		Messages:    []Message{{Role: "user", Content: "test"}},
		Temperature: 0,
		Timeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "exec" {
		t.Fatalf("unexpected tool calls: %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments["node"] != "spark-01" {
		t.Errorf("arg parse failed: %+v", resp.ToolCalls[0].Arguments)
	}
	if resp.Usage.PromptTokens != 40 {
		t.Errorf("usage not captured: %+v", resp.Usage)
	}
}

func TestEndpointOriginSSRF(t *testing.T) {
	cases := []struct {
		name       string
		endpoint   string
		wantOrigin string
		wantErr    bool
	}{
		{
			name:       "localhost /v1/chat/completions (dev default)",
			endpoint:   "http://localhost:4000/v1/chat/completions",
			wantOrigin: "http://localhost:4000",
		},
		{
			name:       "127.0.0.1 /v1/models (loopback exempt)",
			endpoint:   "http://127.0.0.1:4000/v1/models",
			wantOrigin: "http://127.0.0.1:4000",
		},
		{
			name:     "AWS metadata 169.254.169.254 rejected",
			endpoint: "http://169.254.169.254/v1/foo",
			wantErr:  true,
		},
		{
			name:     "10.0.0.1 private range rejected",
			endpoint: "http://10.0.0.1/v1/foo",
			wantErr:  true,
		},
		{
			name:     "192.168.1.1 private range rejected",
			endpoint: "http://192.168.1.1/v1/foo",
			wantErr:  true,
		},
		{
			name:     "172.16.0.1 private range rejected",
			endpoint: "http://172.16.0.1/v1/foo",
			wantErr:  true,
		},
		{
			name:     "ftp scheme rejected",
			endpoint: "ftp://example.com/v1/foo",
			wantErr:  true,
		},
		{
			name:     "empty string rejected",
			endpoint: "",
			wantErr:  true,
		},
		{
			// Bare host without /v1/ is accepted; caller probes and
			// degrades to "unknown" on 404.
			name:       "public host without /v1/ segment returns origin (safe fallback)",
			endpoint:   "http://example.com/no-v1-segment",
			wantOrigin: "http://example.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := endpointOrigin(tc.endpoint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got origin=%q", tc.endpoint, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tc.endpoint, err)
			}
			if got != tc.wantOrigin {
				t.Errorf("origin: got %q want %q", got, tc.wantOrigin)
			}
		})
	}
}

// TestToolCallArgsJSONRoundTrip is a one-shot regression guard ensuring the
// outer Arguments field is a JSON string (not an object) and that its inner
// content decodes back to the original map without double-escaping.
func TestToolCallArgsJSONRoundTrip(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "rt1", Name: "lookup", Arguments: map[string]any{"a": "b"}},
		}},
	}
	out := marshalMessages(msgs)
	if len(out) != 1 || len(out[0].ToolCalls) != 1 {
		t.Fatal("expected one message with one tool call")
	}
	rawBytes := out[0].ToolCalls[0].Function.Arguments

	// 1. Outer value must be a JSON string (starts/ends with quote).
	rawStr := string(rawBytes)
	if len(rawStr) < 2 || rawStr[0] != '"' || rawStr[len(rawStr)-1] != '"' {
		t.Fatalf("outer Arguments must be a JSON-encoded string; got %s", rawStr)
	}

	// 2. Unmarshal the string wrapper.
	var inner string
	if err := json.Unmarshal(rawBytes, &inner); err != nil {
		t.Fatalf("cannot unmarshal outer string: %v", err)
	}

	// 3. Inner content must decode to the expected map.
	var obj map[string]any
	if err := json.Unmarshal([]byte(inner), &obj); err != nil {
		t.Fatalf("inner JSON invalid: %v (inner: %s)", err, inner)
	}
	if obj["a"] != "b" {
		t.Errorf("decoded map missing expected key: %+v", obj)
	}

	// 4. Must NOT be double-escaped: inner string should contain `"a":` literally,
	// not `\"a\":`.
	if !strings.Contains(inner, `"a":`) {
		t.Errorf("inner JSON appears double-escaped; got: %s", inner)
	}
	if strings.Contains(inner, `\"a\":`) {
		t.Errorf("inner JSON is double-escaped (found \\\"a\\\":); got: %s", inner)
	}
}

// TestMarshalMessagesToolCallArgsAsString confirms we double-encode
// tool_calls[*].function.arguments when echoing assistant turns back — spec
// requires a string, not an object.
func TestMarshalMessagesToolCallArgsAsString(t *testing.T) {
	msgs := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "1", Name: "exec", Arguments: map[string]any{"node": "spark-01", "command": "docker ps"}},
		}},
	}
	out := marshalMessages(msgs)
	if len(out) != 1 || len(out[0].ToolCalls) != 1 {
		t.Fatal("expected one message with one tool call")
	}
	raw := string(out[0].ToolCalls[0].Function.Arguments)
	// Must be JSON-encoded string (starts and ends with quote), NOT a raw object.
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		t.Fatalf("arguments should be JSON-encoded string; got %s", raw)
	}
	// The unquoted content should itself be valid JSON.
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("cannot unquote: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil {
		t.Fatalf("inner JSON invalid: %v (inner: %s)", err, s)
	}
	if obj["node"] != "spark-01" {
		t.Errorf("inner object missing node: %+v", obj)
	}
}
